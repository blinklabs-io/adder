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

package plugin

import (
	"context"
	"errors"
	"sync"

	"github.com/blinklabs-io/adder/event"
)

const (
	// DefaultEventBuffer is the event channel buffer a plugin gets when
	// BaseConfig leaves the size unset.
	DefaultEventBuffer = 10
	// ErrorBuffer is the capacity of each Base-owned error channel. It is
	// deliberately not configurable: an unbuffered error channel makes a
	// worker block until the pipeline happens to read, which no plugin
	// wants.
	ErrorBuffer = 16
)

// BaseConfig describes the channels and shutdown behavior a plugin needs
// from Base. A plugin builds one in its StartContext method and hands it to
// StartRun.
type BaseConfig struct {
	// HasInput creates the input event channel. Input-only plugins leave
	// it false so InputChan returns nil.
	HasInput bool
	// InputBuffer is the input channel buffer. Non-positive values mean
	// DefaultEventBuffer.
	InputBuffer int
	// HasOutput creates the output event channel. Output-only plugins
	// leave it false so OutputChan returns nil.
	HasOutput bool
	// OutputBuffer is the output channel buffer. Non-positive values mean
	// DefaultEventBuffer.
	OutputBuffer int
	// DrainOnStop closes the input channel before the shutdown signal so
	// a worker ranging over it processes buffered events on the way out.
	// A worker in a DrainOnStop plugin must exit when its input channel
	// closes and must not rely on Done alone.
	DrainOnStop bool
}

// ShutdownHooks are the optional plugin-specific callbacks Shutdown runs.
type ShutdownHooks struct {
	// BeforeWait runs after workers are signalled to stop but before
	// they are waited on, so it can unblock them: closing a network
	// connection, cancelling a context.
	BeforeWait func() error
	// AfterWait runs once every tracked worker has exited and before the
	// remaining channels close, for resources workers used, such as an open
	// file.
	AfterWait func() error
}

// Base owns the channel plumbing, goroutine tracking and shutdown
// sequencing shared by the built-in plugins. Embed it by value:
//
//	type MyOutput struct {
//	    plugin.Base
//	    format string
//	}
//
// Embedding satisfies the ErrorChan, InputChan and OutputChan methods of
// the ManagedPlugin interface. Base contains a mutex, so a plugin embedding it
// must never be copied.
type Base struct {
	// lifecycleMu covers setup, worker registration, and teardown. mu
	// protects short state transitions; it is released before acquiring
	// lifecycleMu. Workers only take mu, so lifecycle waits can join them.
	lifecycleMu sync.Mutex
	mu          sync.Mutex
	wg          sync.WaitGroup
	// senders counts in-flight Emit and SendError calls that have read a channel
	// and not yet finished sending on it. It exists because wg does not
	// cover all of them: an Emit called from a goroutine a dependency
	// owns, rather than one Go started, is not in wg, so wg.Wait says
	// nothing about it and Shutdown has to wait on this separately
	// before it may close the channel.
	//
	// Add happens under mu while sendClosing is false, which is what
	// makes the count authoritative: once Shutdown sets sendClosing no
	// further sender can join, so a Wait that returns stays returned.
	senders sync.WaitGroup
	// sendClosing rejects new event and error senders. Set by
	// retireUntrackedSenders, cleared by StartRun. Guarded by mu.
	sendClosing bool
	stopped     bool
	// initialized records that StartRun has initialized a run. Unlike
	// running it is never cleared: it separates "shut down" from "never
	// set up", which are otherwise indistinguishable and fail in very
	// different ways.
	initialized  bool
	cfg          BaseConfig
	logger       Logger
	running      bool
	errorChan    chan error
	inputChan    chan event.Event
	inputClosed  bool
	outputChan   chan event.Event
	doneChan     chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	starting     bool
	stopRequests int
	failure      error
	failedChan   chan struct{}
}

// StartRun initializes a plugin once and runs setup while owning its
// lifecycle. The run context is derived from ctx. Repeated calls on a
// healthy running plugin are no-ops and do not replace its context. A failed
// run returns its terminal error until Shutdown completes. Setup must use
// the supplied run context for blocking operations and register workers
// with Go. On failure or cancellation, hooks unwind partial resources
// before StartRun returns.
// Hooks must tolerate partially initialized resources. Neither setup nor
// a tracked worker may call StartRun or Shutdown on this Base.
func (b *Base) StartRun(
	ctx context.Context,
	cfg BaseConfig,
	setup func(context.Context) error,
	hooks ShutdownHooks,
) error {
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	b.mu.Lock()
	if b.stopRequests > 0 {
		b.mu.Unlock()
		return context.Canceled
	}
	if b.running {
		err := b.failure
		b.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		b.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	b.cfg = cfg
	b.errorChan = make(chan error, ErrorBuffer)
	if cfg.HasInput {
		b.inputChan = make(chan event.Event, bufferOr(cfg.InputBuffer))
	}
	if cfg.HasOutput {
		b.outputChan = make(chan event.Event, bufferOr(cfg.OutputBuffer))
	}
	b.doneChan = make(chan struct{})
	b.failedChan = make(chan struct{})
	b.failure = nil
	b.ctx, b.cancel = ctx, cancel
	b.stopped = false
	b.sendClosing = false
	b.running = true
	b.initialized = true
	b.starting = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.starting = false
		b.mu.Unlock()
	}()
	err := ctx.Err()
	if err == nil {
		err = setup(ctx)
	}
	b.mu.Lock()
	if ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		err = errors.Join(err, ctx.Err())
	}
	if b.failure != nil && !errors.Is(err, b.failure) {
		err = errors.Join(err, b.failure)
	}
	if err == nil {
		b.starting = false
	}
	b.mu.Unlock()
	if err != nil {
		return errors.Join(err, b.shutdownLocked(hooks))
	}
	return nil
}

// Shutdown first cancels the run context, including in-progress setup,
// then acquires the lifecycle mutex and runs this sequence once per run:
//
//  1. If DrainOnStop, close the input channel so workers drain it.
//  2. Close the shutdown signal returned by Done.
//  3. hooks.BeforeWait.
//  4. Wait for every goroutine started with Go.
//  5. hooks.AfterWait.
//  6. Retire the senders Go does not track, waiting until they are gone.
//  7. Close the remaining channels.
//
// It returns the first non-nil hook error.
//
// Concurrent callers are serialized by the lifecycle mutex: the sequence
// and its hooks run exactly once, and the callers that lose the race do
// not return until the winner has closed the channels. A nil return
// therefore always means "the workers have exited and the channels are
// closed", never "someone else is still working on it". Once the
// sequence has run, further calls return nil immediately, and StartRun
// re-arms it for the next run.
//
// Never call it from a goroutine started with Go: step 4 would wait on the
// caller's own WaitGroup entry and block forever.
func (b *Base) Shutdown(hooks ShutdownHooks) error {
	// Publish the request before waiting for setup to release lifecycleMu.
	// Pending stops take precedence over another queued StartRun.
	b.mu.Lock()
	b.stopRequests++
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	defer func() {
		b.mu.Lock()
		b.stopRequests--
		b.mu.Unlock()
	}()
	return b.shutdownLocked(hooks)
}

func (b *Base) shutdownLocked(hooks ShutdownHooks) error {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return nil
	}
	b.stopped = true
	b.mu.Unlock()

	var err error
	b.signalStop()
	if hooks.BeforeWait != nil {
		err = hooks.BeforeWait()
	}
	b.wg.Wait()
	if hooks.AfterWait != nil {
		if hookErr := hooks.AfterWait(); err == nil {
			err = hookErr
		}
	}
	// Step 6 has to precede the close and cannot be folded into it: the
	// senders it waits for are the ones the close would otherwise panic.
	b.retireUntrackedSenders()
	b.mu.Lock()
	b.closeChansLocked()
	b.mu.Unlock()
	return err
}

// ErrorChan returns the plugin's error channel.
func (b *Base) ErrorChan() <-chan error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errorChan
}

// InputChan returns the plugin's input event channel, or nil for a
// plugin that does not consume events.
func (b *Base) InputChan() chan<- event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inputChan
}

// OutputChan returns the plugin's output event channel, or nil for a
// plugin that does not produce events.
func (b *Base) OutputChan() <-chan event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.outputChan
}

// Input returns the receive side of the input channel, for the plugin's
// own workers. Capture it once when a worker starts.
func (b *Base) Input() <-chan event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inputChan
}

// Done returns a channel closed when the plugin starts shutting down.
// Capture it once when a worker starts.
func (b *Base) Done() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.doneChan
}

// Context returns the current run's context, canceled by Shutdown or
// cancellation of the parent context. Capture it once for a worker or
// connection attempt.
// Before initialization it returns context.Background().
func (b *Base) Context() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx == nil {
		return context.Background()
	}
	return b.ctx
}

// Go runs fn in a tracked goroutine. Shutdown waits for every goroutine
// started this way.
//
// It panics if StartRun has never initialized the Base. That is
// a programming error with no quiet failure mode
// worth preserving: every accessor would return nil, every sender would
// report false, Start would return nil, and the pipeline would wire
// itself to a black hole. Go is the one method every working plugin
// reaches, so the check lives here and nowhere else.
//
// It does nothing, beyond a warning, if the Base was initialized but is
// not currently running: starting a worker while Shutdown is waiting
// would be a WaitGroup misuse, and the worker would come up after its
// channels were closed. That is a legitimate shutdown race rather than a
// mistake, so it drops the worker instead of panicking. The warning
// needs a logger to be seen, since the symptom is otherwise just a
// plugin that quietly does nothing.
func (b *Base) Go(fn func()) {
	b.mu.Lock()
	if !b.initialized {
		b.mu.Unlock()
		panic(
			"plugin: Go called before StartRun; initialize the run before launching workers",
		)
	}
	if !b.running || b.failure != nil {
		logger := b.logger
		b.mu.Unlock()
		if logger != nil {
			logger.Warn(
				"plugin: Go called while the plugin is not running; " +
					"worker dropped",
			)
		}
		return
	}
	// Registers and starts in one step, and does it while mu is still
	// held: the running check above and the registration have to happen
	// in the same hold. Split them and signalStop can flip running and
	// reach wg.Wait in between, leaving the worker to come up after the
	// channels it uses are closed.
	b.wg.Go(fn)
	b.mu.Unlock()
}

// Wait blocks until every goroutine started with Go has returned.
//
// Never call it from a goroutine started with Go: the caller is itself
// counted, so it would wait for itself forever.
func (b *Base) Wait() {
	b.wg.Wait()
}

// Running reports whether setup completed without a matching Shutdown.
// During setup, after terminal failure, or after failed setup it returns false.
// Parent-context cancellation alone does not change this result.
func (b *Base) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running && !b.starting && b.failure == nil
}

// Emit waits for an output send or run cancellation and reports whether it sent.
// A ready send can win when cancellation is also ready.
//
// It is safe from any goroutine, including ones Base does not track -- a
// callback a dependency invokes on its own goroutine, which wg.Wait knows
// nothing about. Two things together make that true, and neither is
// sufficient alone. The senders WaitGroup holds the close back until every
// in-flight send has returned; Shutdown's wait on it is what a select case
// cannot replace, because a send on an already-closed channel is a ready
// case that panics rather than one select declines to take. The done case
// then guarantees that wait terminates: without it a sender parked on a
// full channel whose consumer has stopped would never return, and the
// close would never come.
func (b *Base) Emit(evt event.Event) bool {
	b.mu.Lock()
	out := b.outputChan
	// A nil channel blocks forever and past sendClosing the channel is
	// about to close, so joining the count would race the Wait already in
	// progress. Either way there is nowhere left to deliver.
	if out == nil || b.sendClosing {
		b.mu.Unlock()
		return false
	}
	b.senders.Add(1)
	done := b.ctx.Done()
	b.mu.Unlock()
	defer b.senders.Done()
	select {
	case <-done:
		return false
	case out <- evt:
		return true
	}
}

// retireUntrackedSenders rejects new Emit and SendError calls and waits
// for in-flight sends before their channels can be closed.
//
// It does not need to drain the channel to get there: every sender aborts
// on the done signal, which signalStop has already closed by the time this
// runs. Those events are dropped, which is the same outcome draining gave
// them -- the consumer is already gone.
//
// The caller must not hold mu.
func (b *Base) retireUntrackedSenders() {
	b.mu.Lock()
	if b.sendClosing {
		b.mu.Unlock()
		return
	}
	b.sendClosing = true
	b.mu.Unlock()
	b.senders.Wait()
}

// SendError waits for a diagnostic send or run cancellation and reports whether
// it sent. A ready send can win against cancellation. Sending does not guarantee
// consumption or persistence. Use Fail for terminal errors.
//
// It is safe from any goroutine, including callbacks racing Shutdown.
func (b *Base) SendError(err error) bool {
	b.mu.Lock()
	ch := b.errorChan
	if ch == nil || b.sendClosing {
		b.mu.Unlock()
		return false
	}
	b.senders.Add(1)
	done := b.ctx.Done()
	b.mu.Unlock()
	defer b.senders.Done()
	select {
	case <-done:
		return false
	case ch <- err:
		return true
	}
}

// TrySendError attempts to send err without waiting for a receiver.
// It reports false when the channel is full or absent, in which case the
// caller should log the error instead.
//
// It is safe from any goroutine, including callbacks racing Shutdown.
func (b *Base) TrySendError(err error) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := b.errorChan
	if ch == nil || b.sendClosing {
		return false
	}
	select {
	case ch <- err:
		return true
	default:
		return false
	}
}

// SetLogger injects the logger without coupling Base to application configuration.
func (b *Base) SetLogger(l Logger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logger = l
}

// Logger returns the logger set with SetLogger, or nil. Plugins that
// want a global fallback keep their own log() helper.
func (b *Base) Logger() Logger {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.logger
}

// signalStop tells the current run's workers to finish, without waiting.
//
// For a DrainOnStop plugin the input channel is closed first and retained:
// such a worker ranges over its input and exits on close, and never looks
// at the done signal, so closing done alone would leave it running
// forever. A registered worker may not have read Input yet, so the closed
// channel must stay available until all workers have exited.
// doneChan is deliberately left in place, closed. A sender that reads it
// after this point sees a closed channel and gives up, rather than
// reading nil and blocking on a send that Shutdown is about to close out
// from under it.
func (b *Base) signalStop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	if b.cancel != nil {
		b.cancel()
	}
	done := b.doneChan
	var drained chan event.Event
	// b.cfg is still the *previous* run's config here, which is the one
	// the running workers were built against.
	if b.cfg.DrainOnStop && b.inputChan != nil {
		drained = b.inputChan
		b.inputClosed = true
	}
	b.mu.Unlock()
	if drained != nil {
		close(drained)
	}
	if done != nil {
		close(done)
	}
}

// closeChansLocked closes and clears any channel still open. The caller
// must hold b.mu.
func (b *Base) closeChansLocked() {
	if b.inputChan != nil {
		if !b.inputClosed {
			close(b.inputChan)
		}
		b.inputChan = nil
	}
	b.inputClosed = false
	if b.outputChan != nil {
		close(b.outputChan)
		b.outputChan = nil
	}
	if b.errorChan != nil {
		close(b.errorChan)
		b.errorChan = nil
	}
}

// bufferOr returns size, or DefaultEventBuffer when size is unset.
func bufferOr(size int) int {
	if size <= 0 {
		return DefaultEventBuffer
	}
	return size
}

// Fail records the first terminal error and cancels this run without joining
// workers. It is safe to call from a worker. Stop is still required for cleanup.
// Failure notification cannot block on an absent error-channel consumer. A
// best-effort copy also goes to ErrorChan for existing diagnostic consumers.
// Errors arriving after cancellation or normal shutdown are ignored.
func (b *Base) Fail(err error) {
	if err == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running || b.failure != nil || b.ctx.Err() != nil {
		return
	}
	b.failure = err
	select {
	case b.errorChan <- err:
	default:
	}
	b.cancel()
	close(b.failedChan)
}

// Failed returns this run's terminal-failure signal, or nil before StartRun.
// Normal shutdown does not close it; obtain it again after restart.
func (b *Base) Failed() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failedChan
}

// Failure returns the first terminal error, retained until the next StartRun.
func (b *Base) Failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failure
}
