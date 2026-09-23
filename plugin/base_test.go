// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plugin_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartRunCreatesOnlyRequestedChannels(t *testing.T) {
	var sink plugin.Base
	plugintest.StartBase(t, &sink, plugin.BaseConfig{HasInput: true})
	assert.NotNil(t, sink.InputChan(), "sink must have an input chan")
	assert.Nil(t, sink.OutputChan(), "sink must not have an output chan")
	assert.NotNil(t, sink.ErrorChan(), "every plugin has an error chan")

	var source plugin.Base
	plugintest.StartBase(t, &source, plugin.BaseConfig{HasOutput: true})
	assert.Nil(t, source.InputChan(), "source must not have an input chan")
	assert.NotNil(t, source.OutputChan(), "source must have an output chan")
}

func TestStartRunAppliesDefaultAndExplicitBuffers(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasInput: true, HasOutput: true},
	)
	assert.Equal(t, plugin.DefaultEventBuffer, cap(b.Input()))
	assert.Equal(t, plugin.DefaultEventBuffer, cap(b.OutputChan()))
	assert.Equal(t, plugin.ErrorBuffer, cap(b.ErrorChan()))

	var big plugin.Base
	plugintest.StartBase(
		t,
		&big,
		plugin.BaseConfig{HasOutput: true, OutputBuffer: 2048},
	)
	assert.Equal(t, 2048, cap(big.OutputChan()))
}

func TestStartRunTreatsANegativeBufferAsTheDefault(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasInput: true, InputBuffer: -1},
	)
	assert.Equal(t, plugin.DefaultEventBuffer, cap(b.InputChan()))
}

func TestStartRunKeepsRunningChannels(t *testing.T) {
	var b plugin.Base
	cfg := plugin.BaseConfig{HasOutput: true}
	plugintest.StartBase(t, &b, cfg)
	first := b.OutputChan()
	firstErr := b.ErrorChan()

	plugintest.StartBase(t, &b, cfg)

	assert.Equal(t, first, b.OutputChan(),
		"duplicate startup must not swap the output chan; the pipeline "+
			"holds a reference to it")
	assert.Equal(t, firstErr, b.ErrorChan())
}

func TestStartRunAfterShutdownCreatesFreshChannels(t *testing.T) {
	var b plugin.Base
	cfg := plugin.BaseConfig{HasOutput: true}
	plugintest.StartBase(t, &b, cfg)
	first := b.OutputChan()
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	plugintest.StartBase(t, &b, cfg)

	assert.NotNil(t, b.OutputChan())
	assert.NotEqual(t, first, b.OutputChan(),
		"after Shutdown the old chan is closed, so StartRun must "+
			"create a new one")
}

func TestShutdownClosesEveryChannel(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasInput: true, HasOutput: true},
	)
	in, out, errs := b.Input(), b.OutputChan(), b.ErrorChan()

	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	for name, ch := range map[string]<-chan event.Event{
		"input": in, "output": out,
	} {
		_, ok := <-ch
		assert.False(t, ok, name+" chan must be closed")
	}
	_, ok := <-errs
	assert.False(t, ok, "error chan must be closed")
	assert.Nil(t, b.InputChan(), "accessors must report nil after stop")
	assert.Nil(t, b.OutputChan())
}

func TestShutdownIsIdempotent(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{HasInput: true})
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
	assert.NotPanics(t, func() {
		_ = b.Shutdown(plugin.ShutdownHooks{})
		_ = b.Shutdown(plugin.ShutdownHooks{})
	}, "double Stop must not double-close")
}

func TestSecondShutdownWaitsForTheFirstToCloseTheChannels(t *testing.T) {
	// A nil return from Shutdown means "the workers are gone and the
	// channels are closed". A plain stopped bool breaks that for the
	// loser of a concurrent pair: it returns nil while the winner is
	// still inside its wait, and a caller that believes it and calls
	// Start rebuilds channels the winner then closes underneath it.
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{HasOutput: true})
	out := b.OutputChan()

	// Park a tracked worker so the first Shutdown sits in its wait until
	// the test releases it.
	release := make(chan struct{})
	b.Go(func() { <-release })

	waiting := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- b.Shutdown(plugin.ShutdownHooks{
			BeforeWait: func() error { close(waiting); return nil },
		})
	}()
	<-waiting

	entered := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		close(entered)
		second <- b.Shutdown(plugin.ShutdownHooks{})
	}()
	<-entered

	select {
	case <-second:
		t.Fatal("the second Shutdown returned while the first was still " +
			"waiting for its workers; its caller would treat a plugin " +
			"with live workers and open channels as fully stopped")
	case <-time.After(250 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-first)
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the second Shutdown never returned")
	}

	_, ok := <-out
	assert.False(t, ok,
		"the channels must be closed by the time the second Shutdown "+
			"returns nil")
}

func TestDoneStaysClosedAfterShutdown(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{HasOutput: true})
	captured := b.Done()
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	select {
	case <-captured:
	default:
		t.Fatal("the captured Done chan must be closed after Shutdown")
	}
	select {
	case <-b.Done():
	case <-time.After(time.Second):
		t.Fatal("Done must stay closed, not become nil: a sender that " +
			"reads it late would block forever instead of giving up")
	}
}

func TestShutdownRunsHooksAroundTheWait(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	var order []string
	var mu sync.Mutex
	record := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}
	// The worker blocks until BeforeWait releases it. That makes the
	// ordering deterministic *and* proves BeforeWait really does run
	// before the wait — if it ran after, Shutdown would hang. This is
	// exactly the chainsync/telegram case: closing the connection or
	// cancelling the poll context is what lets the worker return.
	release := make(chan struct{})
	b.Go(func() {
		<-release
		record("worker")
	})

	err := b.Shutdown(plugin.ShutdownHooks{
		BeforeWait: func() error {
			record("before")
			close(release)
			return nil
		},
		AfterWait: func() error { record("after"); return nil },
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"before", "worker", "after"}, order)
}

func TestShutdownReturnsFirstHookError(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	wantErr := errors.New("closing connection")

	err := b.Shutdown(plugin.ShutdownHooks{
		BeforeWait: func() error { return wantErr },
		AfterWait:  func() error { return errors.New("second") },
	})

	assert.Equal(t, wantErr, err)
}

func TestDrainOnStopFlushesBufferedEvents(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasInput: true, DrainOnStop: true},
	)
	var seen atomic.Int64
	in := b.Input()
	b.Go(func() {
		for range in {
			seen.Add(1)
		}
	})
	for range 5 {
		b.InputChan() <- event.Event{Type: "block"}
	}

	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	assert.Equal(t, int64(5), seen.Load(),
		"buffered events must be processed during shutdown")
}

func TestDrainWorkerReadsInputAfterStopSignal(t *testing.T) {
	for _, operation := range []string{"shutdown", "restart"} {
		t.Run(operation, func(t *testing.T) {
			var b plugin.Base
			cfg := plugin.BaseConfig{HasInput: true, DrainOnStop: true}
			plugintest.StartBase(t, &b, cfg)
			oldInput, done := b.Input(), b.Done()
			b.InputChan() <- event.Event{Type: "block"}
			seen := make(chan []event.Event, 1)
			b.Go(func() {
				// Force the worker's first Input read to happen after the
				// shutdown signal, regardless of scheduler timing.
				<-done
				var events []event.Event
				for evt := range b.Input() {
					events = append(events, evt)
				}
				seen <- events
			})
			finished := make(chan error, 1)
			go func() {
				if operation == "shutdown" {
					finished <- b.Shutdown(plugin.ShutdownHooks{})
					return
				}
				if err := b.Shutdown(plugin.ShutdownHooks{}); err != nil {
					finished <- err
					return
				}
				plugintest.StartBase(t, &b, cfg)
				finished <- nil
			}()
			select {
			case err := <-finished:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown blocked on a drain worker's late Input read")
			}
			assert.Equal(t, []event.Event{{Type: "block"}}, <-seen)
			if operation != "shutdown" {
				require.NotNil(t, b.Input())
				assert.NotEqual(t, oldInput, b.Input())
				b.InputChan() <- event.Event{Type: "next run"}
				assert.Equal(t, "next run", (<-b.Input()).Type)
			}
			require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
			assert.Nil(t, b.Input())
		})
	}
}

func TestWithoutDrainOnStopWorkersExitOnDone(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{HasInput: true})
	var exited atomic.Bool
	done, in := b.Done(), b.Input()
	b.Go(func() {
		for {
			select {
			case <-done:
				exited.Store(true)
				return
			case <-in:
			}
		}
	})

	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	assert.True(t, exited.Load())
}

func TestEmitDeliversAndAbortsOnShutdown(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasOutput: true, OutputBuffer: 1},
	)
	assert.True(t, b.Emit(event.Event{Type: "block"}))
	assert.Equal(t, "block", (<-b.OutputChan()).Type)

	// Fill the buffer, then shut down: the blocked Emit must give up.
	// The emitter runs under Go so Shutdown waits for it before closing
	// the channel — selecting on a send to a closed channel panics.
	require.True(t, b.Emit(event.Event{Type: "block"}))
	emitted := make(chan bool, 1)
	b.Go(func() { emitted <- b.Emit(event.Event{Type: "block"}) })

	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	assert.False(t, <-emitted, "Emit must report the event was dropped")
}

func TestEmitOnAPluginWithNoOutputChanReportsFalse(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{HasInput: true})
	assert.False(t, b.Emit(event.Event{Type: "block"}))
}

// The sender here is deliberately not started with Go. It stands in for a
// dependency's callback goroutine -- gouroboros runs the chainsync
// roll-forward handler on its own recvLoop, which no WaitGroup in adder
// joins -- so wg.Wait returns while this one is still parked on a full
// channel. Closing the channel under it panics that goroutine.
func TestShutdownDoesNotCloseUnderAParkedUntrackedSender(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasOutput: true, OutputBuffer: 1},
	)
	// Fill the buffer so the next send has to park.
	require.True(t, b.Emit(event.Event{Type: "fill"}))

	panics := make(chan any, 1)
	sent := make(chan bool, 1)
	go func() {
		defer func() { panics <- recover() }()
		sent <- b.Emit(event.Event{Type: "parked"})
	}()
	// Let the sender reach the send and block on the full buffer.
	time.Sleep(50 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- b.Shutdown(plugin.ShutdownHooks{}) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal(
			"Shutdown never returned: the done signal must unpark the " +
				"untracked sender, since waiting for a consumer that has " +
				"already stopped never ends",
		)
	}

	require.Nil(t, <-panics,
		"the untracked sender must not be caught by the channel close")
	assert.False(t, <-sent,
		"the parked sender must report the event was dropped, not deliver it")
}

func TestEmitAfterShutdownDrops(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(
		t,
		&b,
		plugin.BaseConfig{HasOutput: true, OutputBuffer: 1},
	)
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
	// Late callers lose the event rather than panicking on the closed
	// channel. A dependency's goroutine can still be running here.
	require.NotPanics(t, func() {
		_ = b.Emit(event.Event{Type: "late"})
	})
}

func TestSendErrorDeliversAndAbortsOnShutdown(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	assert.True(t, b.SendError(errors.New("boom")))
	assert.EqualError(t, <-b.ErrorChan(), "boom")

	for range plugin.ErrorBuffer {
		require.True(t, b.SendError(errors.New("fill")))
	}
	sent := make(chan bool, 1)
	b.Go(func() { sent <- b.SendError(errors.New("blocked")) })

	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	assert.False(t, <-sent, "SendError must give up at shutdown")
}

func TestTrySendErrorNeverBlocks(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	for range plugin.ErrorBuffer {
		assert.True(t, b.TrySendError(errors.New("fill")))
	}
	assert.False(t, b.TrySendError(errors.New("overflow")),
		"a full error chan must drop, not block")
}

func TestTrySendErrorOnAShutDownPluginReportsFalse(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{HasInput: true})
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
	assert.False(t, b.TrySendError(errors.New("boom")))
}

func TestSendersOnAnUninitializedBaseReportFalse(t *testing.T) {
	var b plugin.Base
	assert.False(t, b.Emit(event.Event{Type: "block"}))
	assert.False(t, b.SendError(errors.New("boom")))
	assert.False(t, b.TrySendError(errors.New("boom")))
	assert.False(t, b.Running())
	assert.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
}

func TestGoAndWaitTrackGoroutines(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	var count atomic.Int64
	for range 10 {
		b.Go(func() {
			time.Sleep(10 * time.Millisecond)
			count.Add(1)
		})
	}
	b.Wait()
	assert.Equal(t, int64(10), count.Load())
}

func TestGoOnAStoppedPluginDropsTheWorkerAndWarns(t *testing.T) {
	var b plugin.Base
	l := &captureLogger{}
	b.SetLogger(l)
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	var ran atomic.Bool
	b.Go(func() { ran.Store(true) })
	b.Wait()

	assert.False(t, ran.Load(), "a worker must not start after Shutdown")
	require.Len(t, l.warnings(), 1)
	assert.Contains(t, l.warnings()[0], "worker dropped")
}

func TestGoWithNoLoggerDropsTheWorkerSilently(t *testing.T) {
	var b plugin.Base
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))

	var ran atomic.Bool
	assert.NotPanics(t, func() { b.Go(func() { ran.Store(true) }) })
	b.Wait()
	assert.False(t, ran.Load())
}

func TestGoBeforeStartRunPanics(t *testing.T) {
	// A Start that forgets StartRun is a plugin the pipeline wires up and
	// that then does nothing at all: every worker dropped, every
	// accessor nil, every sender false, and Start still returning nil.
	// Go is the one method every working plugin reaches, so it is where
	// that mistake gets caught.
	var b plugin.Base
	var ran atomic.Bool

	msg := recoverPanic(t, func() { b.Go(func() { ran.Store(true) }) })

	assert.Contains(t, msg, "Go called before StartRun")
	assert.False(t, ran.Load(), "the worker must not have started")
}

func TestRunningTracksTheLifecycle(t *testing.T) {
	var b plugin.Base
	assert.False(t, b.Running(), "a fresh Base is not running")
	plugintest.StartBase(t, &b, plugin.BaseConfig{})
	assert.True(t, b.Running())
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
	assert.False(t, b.Running())
}

func TestLoggerRoundTrips(t *testing.T) {
	var b plugin.Base
	assert.Nil(t, b.Logger(), "Base never invents a logger")
	l := &captureLogger{}
	b.SetLogger(l)
	assert.Same(t, l, b.Logger())
}

// recoverPanic runs fn, fails the test if it did not panic, and returns
// the panic value rendered as a string.
func recoverPanic(t *testing.T, fn func()) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected a panic, got a normal return")
		msg = fmt.Sprint(r)
	}()
	fn()
	return ""
}

// captureLogger records the warnings Base emits. The other levels are
// discarded; no test asserts on them.
type captureLogger struct {
	mu    sync.Mutex
	warns []string
}

func (c *captureLogger) Info(string, ...any) {}

func (c *captureLogger) Warn(msg string, _ ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warns = append(c.warns, msg)
}

func (c *captureLogger) Debug(string, ...any) {}
func (c *captureLogger) Error(string, ...any) {}

func (c *captureLogger) warnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.warns...)
}
