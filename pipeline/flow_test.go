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

package pipeline

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
)

// fakeInput is an input plugin that emits events on demand via emit().
// It owns its output and error channels and recreates them on Start so
// the pipeline can be restarted.
type fakeInput struct {
	outputChan chan event.Event
	errorChan  chan error
	stopOnce   sync.Once
}

func (f *fakeInput) Start() error {
	f.outputChan = make(chan event.Event)
	f.errorChan = make(chan error)
	f.stopOnce = sync.Once{}
	return nil
}

func (f *fakeInput) Stop() error {
	f.stopOnce.Do(func() {
		close(f.outputChan)
		close(f.errorChan)
	})
	return nil
}

func (f *fakeInput) ErrorChan() <-chan error        { return f.errorChan }
func (f *fakeInput) InputChan() chan<- event.Event  { return nil }
func (f *fakeInput) OutputChan() <-chan event.Event { return f.outputChan }

// emit sends an event into the pipeline (blocking handshake).
func (f *fakeInput) emit(evt event.Event) { f.outputChan <- evt }

// emitError sends an error onto the input's error channel.
func (f *fakeInput) emitError(err error) { f.errorChan <- err }

// passFilter forwards every event from InputChan to OutputChan unchanged,
// unless dropTypes contains the event's Type, in which case it is dropped.
type passFilter struct {
	inputChan  chan event.Event
	outputChan chan event.Event
	errorChan  chan error
	doneChan   chan struct{}
	wg         sync.WaitGroup
	stopOnce   sync.Once
	dropTypes  map[string]bool
}

func newPassFilter(dropTypes ...string) *passFilter {
	drop := make(map[string]bool, len(dropTypes))
	for _, t := range dropTypes {
		drop[t] = true
	}
	return &passFilter{dropTypes: drop}
}

func (f *passFilter) Start() error {
	f.inputChan = make(chan event.Event)
	f.outputChan = make(chan event.Event)
	f.errorChan = make(chan error)
	f.doneChan = make(chan struct{})
	f.stopOnce = sync.Once{}
	f.wg.Add(1)
	go func(doneChan <-chan struct{}, inputChan <-chan event.Event, outputChan chan<- event.Event) {
		defer f.wg.Done()
		for {
			select {
			case <-doneChan:
				return
			case evt, ok := <-inputChan:
				if !ok {
					return
				}
				if f.dropTypes[evt.Type] {
					continue
				}
				select {
				case outputChan <- evt:
				case <-doneChan:
					return
				}
			}
		}
	}(f.doneChan, f.inputChan, f.outputChan)
	return nil
}

func (f *passFilter) Stop() error {
	f.stopOnce.Do(func() {
		close(f.doneChan)
		f.wg.Wait()
		close(f.inputChan)
		close(f.outputChan)
		close(f.errorChan)
	})
	return nil
}

func (f *passFilter) ErrorChan() <-chan error        { return f.errorChan }
func (f *passFilter) InputChan() chan<- event.Event  { return f.inputChan }
func (f *passFilter) OutputChan() <-chan event.Event { return f.outputChan }

// collectOutput is an output plugin that pushes every received event onto
// a buffered channel so tests can read them with a real handshake (no
// sleeps). Buffer is large enough for the test volume.
type collectOutput struct {
	inputChan chan event.Event
	errorChan chan error
	received  chan event.Event
	doneChan  chan struct{}
	wg        sync.WaitGroup
	stopOnce  sync.Once
}

func newCollectOutput(buf int) *collectOutput {
	return &collectOutput{received: make(chan event.Event, buf)}
}

func (o *collectOutput) Start() error {
	o.inputChan = make(chan event.Event)
	o.errorChan = make(chan error)
	o.doneChan = make(chan struct{})
	o.stopOnce = sync.Once{}
	o.wg.Add(1)
	go func(doneChan <-chan struct{}, inputChan <-chan event.Event, received chan<- event.Event) {
		defer o.wg.Done()
		for {
			select {
			case <-doneChan:
				return
			case evt, ok := <-inputChan:
				if !ok {
					return
				}
				select {
				case received <- evt:
				case <-doneChan:
					return
				}
			}
		}
	}(o.doneChan, o.inputChan, o.received)
	return nil
}

func (o *collectOutput) Stop() error {
	o.stopOnce.Do(func() {
		close(o.doneChan)
		o.wg.Wait()
		close(o.inputChan)
		close(o.errorChan)
	})
	return nil
}

func (o *collectOutput) ErrorChan() <-chan error        { return o.errorChan }
func (o *collectOutput) InputChan() chan<- event.Event  { return o.inputChan }
func (o *collectOutput) OutputChan() <-chan event.Event { return nil }

// next reads the next received event, failing the test on timeout.
func (o *collectOutput) next(t *testing.T) event.Event {
	t.Helper()
	select {
	case evt := <-o.received:
		return evt
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for output event")
		return event.Event{}
	}
}

// TestFlowInputFilterOutput pushes events through a real
// input -> filter -> output pipeline and asserts they arrive in order
// using channel handshakes (deterministic under -race).
func TestFlowInputFilterOutput(t *testing.T) {
	p := New()
	in := &fakeInput{}
	filter := newPassFilter()
	out := newCollectOutput(10)
	p.AddInput(in)
	p.AddFilter(filter)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			t.Errorf("Stop error: %v", err)
		}
	}()

	in.emit(event.Event{Type: "a"})
	in.emit(event.Event{Type: "b"})

	if got := out.next(t); got.Type != "a" {
		t.Errorf("first event = %q, want a", got.Type)
	}
	if got := out.next(t); got.Type != "b" {
		t.Errorf("second event = %q, want b", got.Type)
	}
}

// TestFlowFilterDrops verifies a filter that drops a specific event type
// prevents it from reaching the output, while a later passing event does
// arrive (a barrier proving the dropped event's absence, not a timeout).
func TestFlowFilterDrops(t *testing.T) {
	p := New()
	in := &fakeInput{}
	filter := newPassFilter("drop-me")
	out := newCollectOutput(10)
	p.AddInput(in)
	p.AddFilter(filter)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			t.Errorf("Stop error: %v", err)
		}
	}()

	in.emit(event.Event{Type: "drop-me"})
	in.emit(event.Event{Type: "keep-me"})

	// The first event to surface at the output must be the kept one; the
	// dropped event never appears.
	got := out.next(t)
	if got.Type != "keep-me" {
		t.Fatalf("output event = %q, want keep-me (drop-me should be dropped)",
			got.Type)
	}
}

// TestFlowNoFilters verifies the pipeline forwards input straight to
// output when no filters are configured.
func TestFlowNoFilters(t *testing.T) {
	p := New()
	in := &fakeInput{}
	out := newCollectOutput(10)
	p.AddInput(in)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			t.Errorf("Stop error: %v", err)
		}
	}()

	in.emit(event.Event{Type: "direct"})
	if got := out.next(t); got.Type != "direct" {
		t.Errorf("event = %q, want direct", got.Type)
	}
}

// TestFlowErrorPropagation verifies an error emitted by an input plugin
// is forwarded to the pipeline ErrorChan().
func TestFlowErrorPropagation(t *testing.T) {
	p := New()
	in := &fakeInput{}
	out := newCollectOutput(1)
	p.AddInput(in)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			t.Errorf("Stop error: %v", err)
		}
	}()

	errCh := p.ErrorChan()
	wantErr := errors.New("boom")
	in.emitError(wantErr)

	select {
	case got := <-errCh:
		if !errors.Is(got, wantErr) {
			t.Errorf("ErrorChan got %v, want %v", got, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error on ErrorChan")
	}
}

// TestFlowErrorChanReObtainAfterRestart verifies the documented contract:
// after Stop()+Start(), the old ErrorChan reference is closed and callers
// must re-obtain a new (working) channel.
func TestFlowErrorChanReObtainAfterRestart(t *testing.T) {
	p := New()
	in := &fakeInput{}
	out := newCollectOutput(1)
	p.AddInput(in)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("first Start error: %v", err)
	}
	oldErrCh := p.ErrorChan()

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	// Old channel reference must be closed after Stop.
	select {
	case _, ok := <-oldErrCh:
		if ok {
			t.Fatal("old ErrorChan should be closed, received a value")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old ErrorChan not closed after Stop")
	}

	// Restart; ErrorChan() must return a fresh, distinct, working channel.
	if err := p.Start(); err != nil {
		t.Fatalf("restart Start error: %v", err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			t.Errorf("final Stop error: %v", err)
		}
	}()

	newErrCh := p.ErrorChan()
	if newErrCh == oldErrCh {
		t.Fatal("ErrorChan() returned the same closed channel after restart")
	}

	wantErr := errors.New("after-restart")
	in.emitError(wantErr)
	select {
	case got := <-newErrCh:
		if !errors.Is(got, wantErr) {
			t.Errorf("new ErrorChan got %v, want %v", got, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error on re-obtained ErrorChan")
	}
}

// TestFlowStartAlreadyRunning verifies that calling Start on a running pipeline
// returns the expected error.
func TestFlowStartAlreadyRunning(t *testing.T) {
	p := New()
	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		_ = p.Stop()
	}()
	if err := p.Start(); err == nil {
		t.Error("expected error when calling Start on already running pipeline")
	}
}

type failStartPlugin struct {
	fakeInput
	fail bool
}

func (f *failStartPlugin) Start() error {
	if f.fail {
		return errors.New("failed to start")
	}
	return f.fakeInput.Start()
}

type failStopPlugin struct {
	fakeInput
	failStop bool
}

func (f *failStopPlugin) Stop() error {
	_ = f.fakeInput.Stop()
	if f.failStop {
		return errors.New("failed to stop")
	}
	return nil
}

// TestFlowStartRollback verifies that if a plugin fails to start, the pipeline
// stops already started plugins and returns the joined startup error. It also
// covers the error collection branch during rollback.
func TestFlowStartRollback(t *testing.T) {
	p := New()
	in1 := &failStopPlugin{failStop: true} // Starts successfully, fails to stop during rollback
	in2 := &failStartPlugin{fail: true}    // Fails to start
	p.AddInput(in1)
	p.AddInput(in2)

	err := p.Start()
	if err == nil {
		t.Fatal("expected error on Start, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "failed to start input") {
		t.Errorf("expected error to contain %q, got %q", "failed to start input", errStr)
	}
	if !strings.Contains(errStr, "failed to stop") {
		t.Errorf("expected error to contain %q, got %q", "failed to stop", errStr)
	}
}

// TestFlowStartFilterFailRollback verifies that if a filter plugin fails to start,
// rollback is invoked and the startup error is returned.
func TestFlowStartFilterFailRollback(t *testing.T) {
	p := New()
	in := &fakeInput{}
	filter := &failStartPlugin{fail: true}
	p.AddInput(in)
	p.AddFilter(filter)

	err := p.Start()
	if err == nil {
		t.Fatal("expected error on Start when filter fails, got nil")
	}
}

// TestFlowStartOutputFailRollback verifies that if an output plugin fails to start,
// rollback is invoked and the startup error is returned.
func TestFlowStartOutputFailRollback(t *testing.T) {
	p := New()
	in := &fakeInput{}
	out := &failStartPlugin{fail: true}
	p.AddInput(in)
	p.AddOutput(out)

	err := p.Start()
	if err == nil {
		t.Fatal("expected error on Start when output fails, got nil")
	}
}

// TestFlowStopPluginErrors verifies that p.Stop() collects and returns errors
// from inputs, filters, and outputs that fail during shutdown.
func TestFlowStopPluginErrors(t *testing.T) {
	p := New()
	in := &failStopPlugin{failStop: true}
	filter := &failStopPlugin{failStop: true}
	out := &failStopPlugin{failStop: true}
	p.AddInput(in)
	p.AddFilter(filter)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	err := p.Stop()
	if err == nil {
		t.Fatal("expected error on Stop, got nil")
	}
	errStr := err.Error()
	for _, expected := range []string{"failed to stop input", "failed to stop filter", "failed to stop output"} {
		if !strings.Contains(errStr, expected) {
			t.Errorf("expected Stop error to aggregate %q, got %q", expected, errStr)
		}
	}
}

// TestFlowObserverChannelFullDrop verifies that when the registered observer's
// channel is full, the pipeline drops the event and continues without blocking.
func TestFlowObserverChannelFullDrop(t *testing.T) {
	p := New()
	in := &fakeInput{}
	p.AddInput(in)

	// Register an unbuffered observer channel and don't read from it.
	obs := make(chan event.Event)
	p.RegisterObserver(obs)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		_ = p.Stop()
	}()

	done := make(chan struct{})
	go func() {
		in.emit(event.Event{Type: "first"})
		in.emit(event.Event{Type: "second"})
		close(done)
	}()

	select {
	case <-done:
		// Succeeded without blocking!
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline blocked on full observer channel")
	}
}

// TestFlowChanCopyLoopBlockedDoneChan verifies the nested select doneChan case
// in chanCopyLoop when the send is blocked.
func TestFlowChanCopyLoopBlockedDoneChan(t *testing.T) {
	p := New()
	input := make(chan event.Event)
	output := make(chan event.Event)

	p.wg.Add(1)
	go p.chanCopyLoop(input, output)

	sent := make(chan struct{})
	// Send an event so it is received by chanCopyLoop and blocks trying to send to output
	go func() {
		input <- event.Event{Type: "block"}
		close(sent)
	}()

	// Wait for the transfer from input to complete (guaranteeing chanCopyLoop is inside the nested select)
	<-sent

	// Stop/close doneChan to unblock it
	close(p.doneChan)
	p.wg.Wait() // Should terminate immediately
}

// TestFlowErrorChanWaitBlockedDoneChan verifies the nested select doneChan case
// in errorChanWait when the send is blocked.
func TestFlowErrorChanWaitBlockedDoneChan(t *testing.T) {
	p := New()
	errorChan := make(chan error)

	p.wg.Add(1)
	go p.errorChanWait(errorChan)

	sent := make(chan struct{})
	// Send an error so it is received by errorChanWait and blocks trying to send to p.errorChan
	go func() {
		errorChan <- errors.New("block-err")
		close(sent)
	}()

	// Wait for transfer from errorChan to complete
	<-sent

	// Stop/close doneChan to unblock it
	close(p.doneChan)
	p.wg.Wait() // Should terminate immediately
}

type blockedOutput struct {
	inputChan chan event.Event
}

func (b *blockedOutput) Start() error {
	b.inputChan = make(chan event.Event)
	return nil
}

func (b *blockedOutput) Stop() error {
	close(b.inputChan)
	return nil
}
func (b *blockedOutput) ErrorChan() <-chan error        { return nil }
func (b *blockedOutput) InputChan() chan<- event.Event  { return b.inputChan }
func (b *blockedOutput) OutputChan() <-chan event.Event { return nil }

// TestFlowOutputChanLoopBlockedDoneChan verifies the nested select doneChan case
// in outputChanLoop when sending to outputs is blocked.
func TestFlowOutputChanLoopBlockedDoneChan(t *testing.T) {
	p := New()
	out := &blockedOutput{}
	_ = out.Start()
	p.AddOutput(out)

	// We start the loop manually
	p.wg.Add(1)
	go p.outputChanLoop()

	sent := make(chan struct{})
	// Send an event to outputChan so it is read and blocks on out.InputChan()
	go func() {
		p.outputChan <- event.Event{Type: "block"}
		close(sent)
	}()

	// Wait for transfer to outputChan to complete
	<-sent

	// Stop/close doneChan to unblock it
	close(p.doneChan)
	p.wg.Wait() // Should terminate immediately
}

// TestFlowTwoFilters verifies the pipeline works correctly with two filters in series,
// covering the else branch in Start() for configuring multiple filters.
func TestFlowTwoFilters(t *testing.T) {
	p := New()
	in := &fakeInput{}
	f1 := newPassFilter()
	f2 := newPassFilter()
	out := newCollectOutput(10)
	p.AddInput(in)
	p.AddFilter(f1)
	p.AddFilter(f2)
	p.AddOutput(out)

	if err := p.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() {
		_ = p.Stop()
	}()

	in.emit(event.Event{Type: "hello"})
	if got := out.next(t); got.Type != "hello" {
		t.Errorf("got event type %q, want hello", got.Type)
	}
}
