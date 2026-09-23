package pipeline

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

type noopPlugin struct{ plugin.Base }

func (*noopPlugin) Role() plugin.PluginType { return plugin.PluginTypeInput }
func (n *noopPlugin) StartContext(ctx context.Context) error {
	return n.StartRun(
		ctx,
		plugin.BaseConfig{HasOutput: true},
		func(context.Context) error { return nil },
		plugin.ShutdownHooks{},
	)
}
func (n *noopPlugin) Stop() error { return n.Shutdown(plugin.ShutdownHooks{}) }

type panicPlugin struct{ noopPlugin }

func (*panicPlugin) Stop() error { panic("stop panic") }

type lifecyclePlugin struct {
	plugin.Base
	role     plugin.PluginType
	startErr error
	starts   int
	stops    int
}

func (p *lifecyclePlugin) Role() plugin.PluginType { return p.role }
func (p *lifecyclePlugin) StartContext(ctx context.Context) error {
	p.starts++
	return p.StartRun(ctx, plugin.BaseConfig{
		HasInput:  p.role != plugin.PluginTypeInput,
		HasOutput: p.role != plugin.PluginTypeOutput,
	}, func(context.Context) error { return p.startErr }, plugin.ShutdownHooks{})
}

func (p *lifecyclePlugin) Stop() error {
	p.stops++
	return p.Shutdown(plugin.ShutdownHooks{})
}
func (p *lifecyclePlugin) counts() (int, int) { return p.starts, p.stops }

func TestStopWithPluginPanic(t *testing.T) {
	p := New()
	pp := &panicPlugin{}
	p.AddInput(pp)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}

	// Stop should panic if plugin.Stop panics, since we don't catch panics
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic when plugin.Stop panics")
		}
	}()
	p.Stop()
}

func TestStopIdempotent(t *testing.T) {
	p := New()
	np := &noopPlugin{}
	p.AddInput(np)

	// Stop should be safe to call multiple times, even without prior Start
	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on first Stop: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on second Stop (idempotent): %v", err)
	}
}

func TestStartFailureRollsBackStartedPlugins(t *testing.T) {
	p := New()
	input := &lifecyclePlugin{role: plugin.PluginTypeInput}
	failing := &lifecyclePlugin{
		role:     plugin.PluginTypeFilter,
		startErr: errors.New("start failed"),
	}
	later := &lifecyclePlugin{role: plugin.PluginTypeOutput}
	p.AddInput(input)
	p.AddFilter(failing)
	p.AddOutput(later)

	if err := p.Start(); err == nil {
		t.Fatal("expected startup failure")
	}
	inputStarts, inputStops := input.counts()
	failingStarts, failingStops := failing.counts()
	laterStarts, laterStops := later.counts()
	if inputStarts != 0 || inputStops != 0 {
		t.Fatalf(
			"input starts/stops = %d/%d, want 0/0",
			inputStarts,
			inputStops,
		)
	}
	if failingStarts != 1 || failingStops != 0 {
		t.Fatalf(
			"failing filter starts/stops = %d/%d, want 1/0",
			failingStarts,
			failingStops,
		)
	}
	if laterStarts != 1 || laterStops != 1 {
		t.Fatalf(
			"output starts/stops = %d/%d, want 1/1",
			laterStarts,
			laterStops,
		)
	}
	if p.IsRunning() {
		t.Fatal("pipeline reported running after failed startup")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop after failed startup: %v", err)
	}
}

func TestPipelineDoubleStartRejected(t *testing.T) {
	p := New()
	input := &lifecyclePlugin{role: plugin.PluginTypeInput}
	p.AddInput(input)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(); err == nil {
		t.Fatal("expected second Start to fail")
	}
	starts, _ := input.counts()
	if starts != 1 {
		t.Fatalf("plugin started %d times, want 1", starts)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineRestart(t *testing.T) {
	p := New()
	np := &noopPlugin{}
	p.AddInput(np)

	// First start
	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on first Start: %v", err)
	}

	// Stop
	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on Stop: %v", err)
	}

	// Second start
	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on second Start: %v", err)
	}

	// Stop again
	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on second Stop: %v", err)
	}
}

// restartablePlugin is a plugin that properly supports restart by recreating channels
type restartablePlugin struct {
	role       plugin.PluginType
	failed     chan struct{}
	exited     chan struct{}
	failure    error
	cancel     context.CancelFunc
	errorChan  chan error
	inputChan  chan event.Event
	outputChan chan event.Event
	doneChan   chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	received   []event.Event
	mu         sync.Mutex
}

func (r *restartablePlugin) StartContext(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.failed = make(chan struct{})
	r.exited = make(chan struct{})
	r.failure = nil
	r.errorChan = make(chan error)
	r.inputChan = make(chan event.Event, 10)
	r.outputChan = make(chan event.Event, 10)
	r.doneChan = make(chan struct{})
	r.stopOnce = sync.Once{}
	r.received = nil
	r.mu.Unlock()

	r.wg.Go(func() {
		defer close(r.exited)
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-r.inputChan:
				if !ok {
					return
				}
				r.mu.Lock()
				r.received = append(r.received, evt)
				r.mu.Unlock()
				select {
				case r.outputChan <- evt:
				case <-ctx.Done():
					return
				}
			}
		}
	})
	return nil
}

func (r *restartablePlugin) Stop() error {
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		if r.doneChan != nil {
			close(r.doneChan)
		}
		// Wait for goroutine to exit before closing other channels
		r.wg.Wait()
		if r.inputChan != nil {
			close(r.inputChan)
		}
		if r.outputChan != nil {
			close(r.outputChan)
		}
		if r.errorChan != nil {
			close(r.errorChan)
		}
	})
	return nil
}

func (r *restartablePlugin) ErrorChan() <-chan error { return r.errorChan }

func (r *restartablePlugin) InputChan() chan<- event.Event {
	if r.role == plugin.PluginTypeInput {
		return nil
	}
	return r.inputChan
}

func (r *restartablePlugin) OutputChan() <-chan event.Event {
	if r.role == plugin.PluginTypeOutput {
		return nil
	}
	return r.outputChan
}
func (r *restartablePlugin) Role() plugin.PluginType { return r.role }
func (r *restartablePlugin) Failed() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

func (r *restartablePlugin) Failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failure
}

func (r *restartablePlugin) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return
	}
	r.failure = err
	r.cancel()
	close(r.failed)
}

var _ plugin.ManagedPlugin = (*restartablePlugin)(nil)

func (r *restartablePlugin) getReceived() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.received)
}

// TestPipelineRestartWithEvents tests the full start -> process events -> stop -> start -> process events -> stop cycle
func TestPipelineRestartWithEvents(t *testing.T) {
	p := New()
	input := &restartablePlugin{role: plugin.PluginTypeInput}
	output := &restartablePlugin{role: plugin.PluginTypeOutput}
	p.AddInput(input)
	p.AddOutput(output)

	// First start
	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on first Start: %v", err)
	}

	// Send an event through the pipeline
	evt1 := event.Event{Type: "test.event1"}
	input.outputChan <- evt1

	// Wait for event to be processed by the output plugin
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		received := output.getReceived()
		if len(received) > 0 && received[0].Type == evt1.Type {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	received := output.getReceived()
	if len(received) != 1 || received[0].Type != evt1.Type {
		t.Fatalf(
			"expected 1 event with type %s after first start, got %d events",
			evt1.Type,
			len(received),
		)
	}

	// Stop
	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on Stop: %v", err)
	}

	// Second start (restart)
	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on second Start (restart): %v", err)
	}

	// Send another event through the pipeline after restart
	evt2 := event.Event{Type: "test.event2"}
	input.outputChan <- evt2

	// Wait for event to be processed by the output plugin
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		received := output.getReceived()
		if len(received) > 0 && received[0].Type == evt2.Type {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	received = output.getReceived()
	if len(received) != 1 || received[0].Type != evt2.Type {
		t.Fatalf(
			"expected 1 event with type %s after restart, got %d events",
			evt2.Type,
			len(received),
		)
	}

	// Stop again
	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on second Stop: %v", err)
	}
}

// TestPipelineObserver tests that a registered observer receives copies of events
func TestPipelineObserver(t *testing.T) {
	p := New()
	input := &restartablePlugin{role: plugin.PluginTypeInput}
	output := &restartablePlugin{role: plugin.PluginTypeOutput}
	p.AddInput(input)
	p.AddOutput(output)

	// Register an observer channel
	observerCh := make(chan event.Event, 10)
	p.RegisterObserver(observerCh)

	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on Start: %v", err)
	}

	// Send events through the pipeline
	evt1 := event.Event{Type: "test.observer1"}
	evt2 := event.Event{Type: "test.observer2"}
	input.outputChan <- evt1
	input.outputChan <- evt2

	// Wait for events on the observer channel
	var observed []event.Event
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(observed) < 2 {
		select {
		case evt := <-observerCh:
			observed = append(observed, evt)
		case <-time.After(10 * time.Millisecond):
		}
	}

	if len(observed) != 2 {
		t.Fatalf("expected 2 observed events, got %d", len(observed))
	}
	if observed[0].Type != evt1.Type {
		t.Fatalf(
			"expected first observed event type %s, got %s",
			evt1.Type,
			observed[0].Type,
		)
	}
	if observed[1].Type != evt2.Type {
		t.Fatalf(
			"expected second observed event type %s, got %s",
			evt2.Type,
			observed[1].Type,
		)
	}

	// Verify output plugin also received the events
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		received := output.getReceived()
		if len(received) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	received := output.getReceived()
	if len(received) != 2 {
		t.Fatalf("expected 2 output events, got %d", len(received))
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on Stop: %v", err)
	}
}

// TestPipelineObserverNilSafe tests that the pipeline works without an observer
func TestPipelineObserverNilSafe(t *testing.T) {
	p := New()
	input := &restartablePlugin{role: plugin.PluginTypeInput}
	output := &restartablePlugin{role: plugin.PluginTypeOutput}
	p.AddInput(input)
	p.AddOutput(output)

	// Do NOT register an observer -- should still work

	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on Start: %v", err)
	}

	evt := event.Event{Type: "test.no-observer"}
	input.outputChan <- evt

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		received := output.getReceived()
		if len(received) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	received := output.getReceived()
	if len(received) != 1 || received[0].Type != evt.Type {
		t.Fatalf(
			"expected 1 event with type %s, got %d events",
			evt.Type,
			len(received),
		)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on Stop: %v", err)
	}
}

// TestPipelineObserverDropsWhenFull tests non-blocking behavior when observer is full
func TestPipelineObserverDropsWhenFull(t *testing.T) {
	p := New()
	input := &restartablePlugin{role: plugin.PluginTypeInput}
	output := &restartablePlugin{role: plugin.PluginTypeOutput}
	p.AddInput(input)
	p.AddOutput(output)

	// Use a channel with buffer size 1 so it fills up quickly
	observerCh := make(chan event.Event, 1)
	p.RegisterObserver(observerCh)

	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on Start: %v", err)
	}

	// Send more events than the observer buffer can hold
	for range 5 {
		input.outputChan <- event.Event{Type: "test.overflow"}
	}

	// Wait for output plugin to receive all events
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		received := output.getReceived()
		if len(received) >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	received := output.getReceived()
	if len(received) != 5 {
		t.Fatalf("expected 5 output events, got %d", len(received))
	}

	// Observer should have received at most 1 event (buffer size)
	// but the pipeline should not have blocked
	var observedCount int
	for {
		select {
		case <-observerCh:
			observedCount++
		default:
			goto done
		}
	}
done:
	if observedCount > 1 {
		t.Fatalf(
			"expected at most 1 buffered observer event, got %d",
			observedCount,
		)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("unexpected error on Stop: %v", err)
	}
}

func TestManagedPluginWithoutBaseOrStart(t *testing.T) {
	p := New()
	source := &restartablePlugin{role: plugin.PluginTypeInput}
	sink := &restartablePlugin{role: plugin.PluginTypeOutput}
	p.AddInput(source)
	p.AddOutput(sink)
	t.Cleanup(func() {
		if err := p.Stop(); err != nil {
			t.Error(err)
		}
	})
	for range 2 {
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
		if p.Failure() != nil {
			t.Fatal("previous failure survived restart")
		}
		want := errors.New("custom sink failed")
		sink.fail(want)
		select {
		case <-p.Failed():
		case <-time.After(time.Second):
			t.Fatal("custom failure did not reach pipeline")
		}
		if !errors.Is(p.Failure(), want) {
			t.Fatalf("failure = %v", p.Failure())
		}
		for _, component := range []*restartablePlugin{source, sink} {
			select {
			case <-component.exited:
			case <-time.After(time.Second):
				t.Fatal("worker did not observe cancellation")
			}
		}
		if err := p.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := p.Stop(); err != nil {
			t.Fatal(err)
		}
		if _, open := <-source.OutputChan(); open {
			t.Fatal("Stop returned before closing output")
		}
	}
}
