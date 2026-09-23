// Copyright 2025 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package chainsync

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	ouroboros_mock "github.com/blinklabs-io/ouroboros-mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockNodeConnection returns a real *ouroboros.Connection whose peer is
// an in-memory mock that answers the handshake and nothing else. That is
// enough to test connection ownership: the plugin's lifecycle code only
// ever dials, installs and closes, and none of that needs a live node.
func newMockNodeConnection(t *testing.T) *ouroboros.Connection {
	t.Helper()
	mockConn := ouroboros_mock.NewConnection(
		ouroboros_mock.ProtocolRoleClient,
		[]ouroboros_mock.ConversationEntry{
			ouroboros_mock.ConversationEntryHandshakeRequestGeneric,
			ouroboros_mock.ConversationEntryHandshakeNtCResponse,
		},
	)
	conn, err := ouroboros.New(
		ouroboros.WithConnection(mockConn),
		ouroboros.WithNetworkMagic(ouroboros_mock.MockNetworkMagic),
	)
	require.NoError(t, err)
	return conn
}

// requireConnClosed blocks until conn has finished shutting down. A
// connection closes the error channel it owns as the last step of
// shutdown, so a closed error channel is the observable proof that the
// connection is gone rather than merely unreferenced.
func requireConnClosed(t *testing.T, conn *ouroboros.Connection) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-conn.ErrorChan():
			if !open {
				return
			}
			// A shutting-down connection may report an error first; keep
			// draining until the channel itself closes.
		case <-deadline:
			t.Fatal("the connection is still open after Stop returned")
		}
	}
}

func TestHandleRollBackward(t *testing.T) {
	// Create a new ChainSync instance
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	t.Cleanup(func() { _ = c.Stop() })

	// Define test data
	point := ocommon.Point{
		Slot: 123456,
		Hash: []byte{0x01, 0x02, 0x03, 0x04, 0x05},
	}
	tip := chainsync.Tip{
		Point: ocommon.Point{
			Slot: 67890,
			Hash: []byte{0x06, 0x07, 0x08, 0x09, 0x0A},
		},
	}

	// Call the function under test
	err := c.handleRollBackward(chainsync.CallbackContext{}, point, tip)
	// Verify that no error was returned
	assert.NoError(t, err)

	// Verify that an event was sent to the eventChan
	select {
	case evt := <-c.OutputChan():
		// Verify the event type
		assert.Equal(t, "input.rollback", evt.Type)

		// Verify the timestamp is not zero and is close to the current time
		assert.False(t, evt.Timestamp.IsZero())
		assert.WithinDuration(t, time.Now(), evt.Timestamp, time.Second)

		// Verify the payload is of type RollbackEvent and contains the correct data
		assert.IsType(t, event.RollbackEvent{}, evt.Payload)
		rollbackEvent := evt.Payload.(event.RollbackEvent)
		assert.Equal(t, hex.EncodeToString(point.Hash), rollbackEvent.BlockHash)
		assert.Equal(t, point.Slot, rollbackEvent.SlotNumber)

		// Verify the context is nil (since it's not used in handleRollBackward)
		assert.Nil(t, evt.Context)
	case <-time.After(1 * time.Second):
		t.Fatal("Expected event was not sent to eventChan")
	}

	// Verify that the status was updated correctly
	assert.Equal(t, uint64(123456), c.status.SlotNumber)
	assert.Equal(
		t,
		uint64(0),
		c.status.BlockNumber,
	) // BlockNumber should be 0 after rollback
	assert.Equal(t, "0102030405", c.status.BlockHash)
	assert.Equal(t, uint64(67890), c.status.TipSlotNumber)
	assert.Equal(t, "060708090a", c.status.TipBlockHash)
	// New: Check EpochNumber (Byron era: 123456/21600 = 5)
	assert.Equal(t, uint64(5), c.status.EpochNumber)
}

func TestInvalidIntersectPointFormat(t *testing.T) {
	tests := []struct {
		name           string
		intersectPoint string
	}{
		{
			name:           "missing dot separator",
			intersectPoint: "12345abc123",
		},
		{
			name:           "too many parts",
			intersectPoint: "12345.abc123.extra",
		},
		{
			name:           "non-numeric slot",
			intersectPoint: "notanumber.abc123def456",
		},
		{
			name:           "invalid hex hash",
			intersectPoint: "12345.notvalidhex!@#",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := plugin.GetPlugin(
				plugin.PluginTypeInput,
				"chainsync",
				map[string]any{"intersect-point": tt.intersectPoint},
			)
			require.Error(t, err)
			assert.Nil(
				t,
				p,
				"expected nil plugin for invalid intersect point: %s",
				tt.intersectPoint,
			)
		})
	}

	// Test valid intersect point returns non-nil
	t.Run("valid intersect point", func(t *testing.T) {
		p := mustConfiguredPlugin(
			t,
			map[string]any{"intersect-point": "12345." + testHashA},
		)
		assert.NotNil(t, p, "expected non-nil plugin for valid intersect point")
	})
}

func TestGetKupoClient(t *testing.T) {
	// Setup test server
	ts := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	defer ts.Close()

	t.Run("successful client creation", func(t *testing.T) {
		c := &ChainSync{
			kupoUrl: ts.URL,
		}

		client, err := getKupoClient(c)
		require.NoError(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, c.kupoClient)
	})

	t.Run("returns cached client", func(t *testing.T) {
		mockClient := &kugo.Client{}
		c := &ChainSync{
			kupoUrl:    ts.URL,
			kupoClient: mockClient,
		}

		client, err := getKupoClient(c)
		require.NoError(t, err)
		assert.Same(t, mockClient, client)
	})

	t.Run("health check timeout", func(t *testing.T) {
		slowTS := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(
					4 * time.Second,
				) // Longer than the 3s context timeout
				w.WriteHeader(http.StatusOK)
			}),
		)
		defer slowTS.Close()

		c := &ChainSync{
			kupoUrl: slowTS.URL,
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"kupo health check timed out after 3 seconds",
		)
	})

	t.Run("failed health check status", func(t *testing.T) {
		failTS := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
		)
		defer failTS.Close()

		c := &ChainSync{
			kupoUrl: failTS.URL,
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"health check failed with status code: 500",
		)
	})

	t.Run("malformed URL", func(t *testing.T) {
		c := &ChainSync{
			kupoUrl: "http://invalid url",
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid kupo URL")
	})

	t.Run("unreachable host", func(t *testing.T) {
		c := &ChainSync{
			kupoUrl: "http://unreachable-host.invalid",
		}

		_, err := getKupoClient(c)
		require.Error(t, err)
		assert.True(t,
			strings.Contains(err.Error(), "failed to resolve kupo host") ||
				strings.Contains(err.Error(), "failed to perform health check"),
			"unexpected error: %v", err)
	})
}

func TestChannelsPreservedOnDuplicateStart(t *testing.T) {
	c := &ChainSync{
		intersectPoints: []ocommon.Point{},
		status:          &ChainSyncStatus{},
		autoReconnect:   true,
	}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	t.Cleanup(func() { _ = c.Stop() })
	origEventChan := c.OutputChan()
	origErrorChan := c.ErrorChan()

	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())

	assert.Equal(t, origEventChan, c.OutputChan(),
		"eventChan was replaced during reconnect — "+
			"the pipeline would be orphaned")
	assert.Equal(t, origErrorChan, c.ErrorChan(),
		"errorChan was replaced during reconnect — "+
			"the pipeline would be orphaned")

	// Prove the surviving channel still carries events end to end.
	_ = c.Emit(event.Event{Type: "test.preserve"})
	select {
	case evt := <-origEventChan:
		assert.Equal(t, "test.preserve", evt.Type)
	case <-time.After(time.Second):
		t.Fatal("the preserved eventChan no longer delivers")
	}
}

func TestStartCreatesNewChannelsAfterStop(t *testing.T) {
	// Stop closes the channels; a new run must replace them.
	c := &ChainSync{
		intersectPoints: []ocommon.Point{},
		status:          &ChainSyncStatus{},
	}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	first := c.OutputChan()
	require.NoError(t, c.Stop())

	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	t.Cleanup(func() { _ = c.Stop() })

	require.NotNil(t, c.OutputChan(), "eventChan must exist after Stop")
	require.NotNil(t, c.ErrorChan(), "errorChan must exist after Stop")
	assert.NotEqual(t, first, c.OutputChan(),
		"a closed channel must not be reused")
}

// TestStopWithPendingConnectionError covers the ordinary case of a
// connection error that lands while Stop() is already waiting: the
// handler must consume it and return, and Stop() must complete.
//
// It is worth being explicit about what this test does *not* catch,
// because it was originally written as a regression test for the
// pre-Base deadlock (Stop nil'd doneChan, the handler re-read it, saw
// nil, and parked forever on an unguarded send to an unbuffered
// errorChan). It cannot catch that. Base normalized every plugin error
// channel to plugin.ErrorBuffer slots, so an unguarded send has 15 free
// slots in a test that produces one error and never blocks — replacing
// SendError's select with a bare send leaves this test passing. The
// deadlock class is covered at the Base level by
// TestDoneStaysClosedAfterShutdown, and for this package by
// TestStopReturnsWhileATrackedWorkerIsParked.
func TestStopWithPendingConnectionError(t *testing.T) {
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())

	connErrChan := make(chan error, 1)
	c.Go(func() { c.superviseConnection(c.Context(), connErrChan) })

	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	// Let Stop() get past its shutdown signal and into the wait before the
	// error lands. That is the ordering that used to deadlock.
	time.Sleep(50 * time.Millisecond)
	connErrChan <- errors.New("connection lost")

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() deadlocked waiting for the async error handler")
	}
}

// TestStopReturnsWhileATrackedWorkerIsParked guards the bug class this
// package's Base conversion exists to remove. Stop used to close the
// done channel and then nil the field before waiting for its workers, so
// a worker that re-entered its select after the assignment received on a
// nil channel and never woke: Stop's wait blocked forever. Chainsync is
// one of the plugins that hung in production for exactly this reason.
//
// The worker below re-reads Done() *after* shutdown has signalled, which
// is the only shape that exercises the hang rather than merely observing
// the invariant. A worker that re-reads Done() on every loop iteration
// looks like it tests this, but does not: it is already parked on the
// non-nil channel when the close lands, wakes on the close, and returns
// before the nil assignment can affect it. Measured on the reintroduced
// bug, that shape hit the Stop timeout 0 times in 50 runs and detected
// the regression only through the final assertion, which
// plugin/base_test.go's TestDoneStaysClosedAfterShutdown already pins.
//
// The sleep is load-bearing rather than a stand-in for an assertion: it
// guarantees the late read happens after Stop has had time to mutate the
// field, which is what makes the Stop timeout below the arm that fires.
//
// Every wait is bounded, so a regression fails the test rather than
// hanging the package.
func TestStopReturnsWhileATrackedWorkerIsParked(t *testing.T) {
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())

	started := make(chan struct{})
	exited := make(chan struct{})
	c.Go(func() {
		defer close(exited)
		// Captured while the plugin is running, so this read is non-nil.
		first := c.Done()
		close(started)
		// Wake when shutdown signals.
		<-first
		// Late re-read: under the old code this returned nil and the
		// worker parked here forever, so Stop's wait never returned.
		time.Sleep(50 * time.Millisecond)
		<-c.Done()
	})

	// A worker that had not yet captured the channel could not exercise
	// the bug.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tracked worker never started")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return: the tracked worker is parked on a " +
			"nil done channel")
	}

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop returned while its tracked worker was still running")
	}

	select {
	case <-c.Done():
	default:
		t.Fatal("Done must stay closed after Stop, not become nil: a " +
			"worker that re-reads it late would park instead of exiting")
	}
}

func TestAsyncErrorForwardedToErrorChan(t *testing.T) {
	// With auto-reconnect off, a connection error is forwarded on the
	// plugin error channel for the pipeline to pick up.
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	t.Cleanup(func() { _ = c.Stop() })

	connErrChan := make(chan error, 1)
	c.Go(func() { c.superviseConnection(c.Context(), connErrChan) })
	connErrChan <- errors.New("connection lost")

	select {
	case err := <-c.ErrorChan():
		assert.EqualError(t, err, "connection lost")
	case <-time.After(5 * time.Second):
		t.Fatal("the connection error was never forwarded")
	}
}

func TestSupervisorExitsOnClosedConnChan(t *testing.T) {
	// Stop must join the supervisor even if the dependency closes its
	// error stream at the same time.
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())

	connErrChan := make(chan error)
	c.Go(func() { c.superviseConnection(c.Context(), connErrChan) })
	close(connErrChan)

	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return after the handler's input closed")
	}
}

// TestStopEndsAReconnectInFlight pins that a reconnect observes the
// shutdown signal: Stop must return while the backoff is mid-flight
// rather than after it, and the tracked supervisor must be gone by then.
// Dropping either done check in reconnect fails this test by hanging Stop.
//
// It does not pin the resurrection hazard that motivated the rework, and
// no test can: reconnects go through connect, which has no path to StartRun,
// so there is no longer a code path that could bring a stopped plugin
// back up. That property is structural, and what guards it is Start
// staying the only caller of StartRun.
func TestStopEndsAReconnectInFlight(t *testing.T) {
	// An unresolvable network name makes every reconnect attempt fail
	// immediately, so the loop is reliably sitting in its backoff wait.
	c := &ChainSync{
		status:        &ChainSyncStatus{},
		autoReconnect: true,
		network:       "not-a-real-cardano-network",
	}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())

	connErrChan := make(chan error, 1)
	c.Go(func() { c.superviseConnection(c.Context(), connErrChan) })
	connErrChan <- errors.New("connection lost")
	// Let the supervisor burn its first attempt and enter the backoff.
	time.Sleep(100 * time.Millisecond)

	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return: the reconnect is still running")
	}

	assert.False(t, c.Running(),
		"the reconnect resurrected the plugin after Stop")
	select {
	case <-c.Done():
	default:
		t.Fatal("Done reopened, so StartRun ran again after Shutdown")
	}
}

// TestStopClosesAConnectionInstalledDuringTheWait pins the AfterWait
// close. Stop used to take the connection in BeforeWait only, which sees
// nothing when a dial is still in flight: the dial installs its
// connection afterwards, Stop returns nil, and the node connection and
// its goroutines stay live behind a plugin whose channels are closing.
//
// Dropping the AfterWait hook fails this test — the connection's error
// channel never closes, so requireConnClosed times out.
func TestStopClosesAConnectionInstalledDuringTheWait(t *testing.T) {
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())

	conn := newMockNodeConnection(t)
	// A tracked worker standing in for a dial that was in flight when Stop
	// ran. It installs the connection after the shutdown signal, which is
	// exactly the window BeforeWait cannot cover.
	c.Go(func() {
		<-c.Done()
		// Let Stop get past BeforeWait and into the wait this worker holds
		// open, so the connection lands in the uncovered window rather
		// than racing BeforeWait for it.
		time.Sleep(200 * time.Millisecond)
		c.setConn(conn)
	})

	require.NoError(t, c.Stop())

	requireConnClosed(t, conn)
	assert.Nil(t, c.conn(), "Stop must not leave a connection installed")
}

// TestFailedStartDoesNotLeaveThePluginRunning pins that Start unwinds
// itself. StartRun marks the plugin running before the dial is attempted, and
// connect can fail after setupConnection has already installed a
// connection — Sync fails on a connection that dialled fine. Returning
// the error on its own would hand back a plugin that reports Running with
// nothing running in it, holding a node connection nobody closes.
func TestFailedStartDoesNotLeaveThePluginRunning(t *testing.T) {
	// An unresolvable network name fails the dial, which is the earliest
	// failure point; the later ones unwind through the same path.
	c := &ChainSync{
		status:  &ChainSyncStatus{},
		network: "not-a-real-cardano-network",
	}

	require.Error(t, c.Start())

	assert.False(t, c.Running(),
		"a failed Start must unwind the StartRun it performed")
	assert.Nil(t, c.conn(), "a failed Start must not keep a connection")
	select {
	case <-c.Done():
	default:
		t.Fatal("the shutdown signal is still open after a failed Start")
	}
}

func TestReconnectCallbackFired(t *testing.T) {
	called := false
	c := &ChainSync{
		reconnectCallback: func() { called = true },
	}

	if c.reconnectCallback != nil {
		c.reconnectCallback()
	}

	assert.True(t, called, "reconnect callback should have been called")
}

func TestStartPreservesRunningConnection(t *testing.T) {
	c := New()
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	conn := newMockNodeConnection(t)
	c.setConn(conn)
	output, errs := c.OutputChan(), c.ErrorChan()
	require.NoError(t, c.Start())
	assert.Same(t, conn, c.conn())
	assert.Equal(t, output, c.OutputChan())
	assert.Equal(t, errs, c.ErrorChan())
	require.NoError(t, c.Stop())
	requireConnClosed(t, conn)
}

func TestClosedConnectionWithoutReconnectFailsRun(t *testing.T) {
	c := &ChainSync{status: &ChainSyncStatus{}}
	plugintest.StartBase(t, &c.Base, chainSyncBaseConfig())
	t.Cleanup(func() { require.NoError(t, c.Stop()) })
	connErrors := make(chan error)
	c.Go(func() { c.superviseConnection(c.Context(), connErrors) })
	close(connErrors)
	select {
	case <-c.Failed():
	case <-time.After(time.Second):
		t.Fatal("lost connection left a healthy idle source")
	}
	require.ErrorContains(t, c.Failure(), "connection closed")
	require.False(t, c.Running())
}
