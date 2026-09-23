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
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

type fakeInput struct{ plugin.Base }

func (*fakeInput) Role() plugin.PluginType { return plugin.PluginTypeInput }
func (f *fakeInput) StartContext(ctx context.Context) error {
	return f.StartRun(
		ctx,
		plugin.BaseConfig{HasOutput: true},
		func(context.Context) error { return nil },
		plugin.ShutdownHooks{},
	)
}

func (f *fakeInput) Stop() error          { return f.Shutdown(plugin.ShutdownHooks{}) }
func (f *fakeInput) emit(evt event.Event) { f.Emit(evt) }
func (f *fakeInput) emitError(err error)  { f.SendError(err) }

type passFilter struct {
	plugin.Base
	dropTypes map[string]bool
}

func (*passFilter) Role() plugin.PluginType { return plugin.PluginTypeFilter }
func newPassFilter(dropTypes ...string) *passFilter {
	drop := make(map[string]bool, len(dropTypes))
	for _, t := range dropTypes {
		drop[t] = true
	}
	return &passFilter{dropTypes: drop}
}

func (f *passFilter) StartContext(ctx context.Context) error {
	return f.StartRun(
		ctx,
		plugin.BaseConfig{HasInput: true, HasOutput: true},
		func(ctx context.Context) error {
			input := f.Input()
			f.Go(func() {
				for {
					select {
					case <-ctx.Done():
						return
					case evt := <-input:
						if !f.dropTypes[evt.Type] && !f.Emit(evt) {
							return
						}
					}
				}
			})
			return nil
		},
		plugin.ShutdownHooks{},
	)
}
func (f *passFilter) Stop() error { return f.Shutdown(plugin.ShutdownHooks{}) }

type collectOutput struct {
	plugin.Base
	received chan event.Event
}

func (*collectOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func newCollectOutput(buf int) *collectOutput {
	return &collectOutput{received: make(chan event.Event, buf)}
}

func (o *collectOutput) StartContext(ctx context.Context) error {
	return o.StartRun(
		ctx,
		plugin.BaseConfig{HasInput: true},
		func(ctx context.Context) error {
			input := o.Input()
			o.Go(func() {
				for {
					select {
					case <-ctx.Done():
						return
					case evt := <-input:
						select {
						case o.received <- evt:
						case <-ctx.Done():
							return
						}
					}
				}
			})
			return nil
		},
		plugin.ShutdownHooks{},
	)
}

func (o *collectOutput) Stop() error { return o.Shutdown(plugin.ShutdownHooks{}) }

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
	plugin.Base
	role plugin.PluginType
	fail bool
}

func (f *failStartPlugin) Role() plugin.PluginType { return f.role }
func (f *failStartPlugin) StartContext(ctx context.Context) error {
	return f.StartRun(ctx, plugin.BaseConfig{
		HasInput:  f.role != plugin.PluginTypeInput,
		HasOutput: f.role != plugin.PluginTypeOutput,
	}, func(context.Context) error {
		if f.fail {
			return errors.New("failed to start")
		}
		return nil
	}, plugin.ShutdownHooks{})
}

func (f *failStartPlugin) Stop() error { return f.Shutdown(plugin.ShutdownHooks{}) }

type failStopPlugin struct {
	failStartPlugin
	failStop bool
}

func (f *failStopPlugin) Stop() error {
	return f.Shutdown(plugin.ShutdownHooks{AfterWait: func() error {
		if f.failStop {
			return errors.New("failed to stop")
		}
		return nil
	}})
}

// TestFlowStartRollback verifies that if a plugin fails to start, the pipeline
// stops already started plugins and returns the joined startup error. It also
// covers the error collection branch during rollback.
func TestFlowStartRollback(t *testing.T) {
	p := New()
	in1 := &failStopPlugin{
		failStartPlugin: failStartPlugin{role: plugin.PluginTypeInput},
		failStop:        true,
	} // Starts successfully, fails to stop during rollback
	in2 := &failStartPlugin{
		role: plugin.PluginTypeInput,
		fail: true,
	} // Fails to start
	p.AddInput(in1)
	p.AddInput(in2)

	err := p.Start()
	if err == nil {
		t.Fatal("expected error on Start, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "failed to start input") {
		t.Errorf(
			"expected error to contain %q, got %q",
			"failed to start input",
			errStr,
		)
	}
	if !strings.Contains(errStr, "failed to stop") {
		t.Errorf(
			"expected error to contain %q, got %q",
			"failed to stop",
			errStr,
		)
	}
}

// TestFlowStartFilterFailRollback verifies that if a filter plugin fails to start,
// rollback is invoked and the startup error is returned.
func TestFlowStartFilterFailRollback(t *testing.T) {
	p := New()
	in := &fakeInput{}
	filter := &failStartPlugin{role: plugin.PluginTypeFilter, fail: true}
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
	out := &failStartPlugin{role: plugin.PluginTypeOutput, fail: true}
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
	in := &failStopPlugin{
		failStartPlugin: failStartPlugin{role: plugin.PluginTypeInput},
		failStop:        true,
	}
	filter := &failStopPlugin{
		failStartPlugin: failStartPlugin{role: plugin.PluginTypeFilter},
		failStop:        true,
	}
	out := &failStopPlugin{
		failStartPlugin: failStartPlugin{role: plugin.PluginTypeOutput},
		failStop:        true,
	}
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
			t.Errorf(
				"expected Stop error to aggregate %q, got %q",
				expected,
				errStr,
			)
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
		for range 10000 {
			in.emit(event.Event{Type: "unobserved"})
		}
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
	defer out.Stop()

	// We start the loop manually
	p.wg.Add(1)
	go p.outputChanLoop([]chan<- event.Event{out.InputChan()})

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
