package pipeline

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
)

// plugin that panics when Stop is called
type panicPlugin struct {
	errChanOnce sync.Once
	errChan     chan error
}

func (p *panicPlugin) Start() error { return nil }
func (p *panicPlugin) Stop() error  { panic("stop panic") }

func (p *panicPlugin) ErrorChan() <-chan error {
	p.errChanOnce.Do(func() {
		if p.errChan == nil {
			p.errChan = make(chan error)
		}
	})
	return p.errChan
}
func (p *panicPlugin) InputChan() chan<- event.Event  { return nil }
func (p *panicPlugin) OutputChan() <-chan event.Event { return nil }

// simple no-op plugin
type noopPlugin struct {
	errChanOnce sync.Once
	errChan     chan error
}

type lifecyclePlugin struct {
	mu         sync.Mutex
	startErr   error
	starts     int
	stops      int
	errChan    chan error
	inputChan  chan event.Event
	outputChan chan event.Event
}

func (p *lifecyclePlugin) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts++
	if p.startErr != nil {
		return p.startErr
	}
	p.errChan = make(chan error)
	p.inputChan = make(chan event.Event)
	p.outputChan = make(chan event.Event)
	return nil
}

func (p *lifecyclePlugin) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	return nil
}

func (p *lifecyclePlugin) ErrorChan() <-chan error        { return p.errChan }
func (p *lifecyclePlugin) InputChan() chan<- event.Event  { return p.inputChan }
func (p *lifecyclePlugin) OutputChan() <-chan event.Event { return p.outputChan }

func (p *lifecyclePlugin) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts, p.stops
}

func (n *noopPlugin) Start() error { return nil }
func (n *noopPlugin) Stop() error  { return nil }
func (n *noopPlugin) ErrorChan() <-chan error {
	n.errChanOnce.Do(func() {
		if n.errChan == nil {
			n.errChan = make(chan error)
		}
	})
	return n.errChan
}
func (n *noopPlugin) InputChan() chan<- event.Event  { return nil }
func (n *noopPlugin) OutputChan() <-chan event.Event { return nil }

func TestStopWithPluginPanic(t *testing.T) {
	p := New()
	pp := &panicPlugin{}
	p.AddInput(pp)

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
	input := &lifecyclePlugin{}
	failing := &lifecyclePlugin{startErr: errors.New("start failed")}
	later := &lifecyclePlugin{}
	p.AddInput(input)
	p.AddFilter(failing)
	p.AddOutput(later)

	if err := p.Start(); err == nil {
		t.Fatal("expected startup failure")
	}
	inputStarts, inputStops := input.counts()
	failingStarts, failingStops := failing.counts()
	laterStarts, laterStops := later.counts()
	if inputStarts != 1 || inputStops != 1 {
		t.Fatalf("input starts/stops = %d/%d, want 1/1", inputStarts, inputStops)
	}
	if failingStarts != 1 || failingStops != 0 {
		t.Fatalf("failing filter starts/stops = %d/%d, want 1/0", failingStarts, failingStops)
	}
	if laterStarts != 0 || laterStops != 0 {
		t.Fatalf("later output starts/stops = %d/%d, want 0/0", laterStarts, laterStops)
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
	input := &lifecyclePlugin{}
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
	errorChan  chan error
	inputChan  chan event.Event
	outputChan chan event.Event
	doneChan   chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	received   []event.Event
	mu         sync.Mutex
}

func (r *restartablePlugin) Start() error {
	r.mu.Lock()
	r.errorChan = make(chan error)
	r.inputChan = make(chan event.Event, 10)
	r.outputChan = make(chan event.Event, 10)
	r.doneChan = make(chan struct{})
	r.stopOnce = sync.Once{}
	r.received = nil
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			select {
			case <-r.doneChan:
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
				case <-r.doneChan:
					return
				}
			}
		}
	}()
	return nil
}

func (r *restartablePlugin) Stop() error {
	r.stopOnce.Do(func() {
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

func (r *restartablePlugin) InputChan() chan<- event.Event { return r.inputChan }

func (r *restartablePlugin) OutputChan() <-chan event.Event { return r.outputChan }

func (r *restartablePlugin) getReceived() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]event.Event, len(r.received))
	copy(result, r.received)
	return result
}

// TestPipelineRestartWithEvents tests the full start -> process events -> stop -> start -> process events -> stop cycle
func TestPipelineRestartWithEvents(t *testing.T) {
	p := New()
	input := &restartablePlugin{}
	output := &restartablePlugin{}
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
	input := &restartablePlugin{}
	output := &restartablePlugin{}
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
	input := &restartablePlugin{}
	output := &restartablePlugin{}
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
	input := &restartablePlugin{}
	output := &restartablePlugin{}
	p.AddInput(input)
	p.AddOutput(output)

	// Use a channel with buffer size 1 so it fills up quickly
	observerCh := make(chan event.Event, 1)
	p.RegisterObserver(observerCh)

	if err := p.Start(); err != nil {
		t.Fatalf("unexpected error on Start: %v", err)
	}

	// Send more events than the observer buffer can hold
	for i := 0; i < 5; i++ {
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
