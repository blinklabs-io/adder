package pipeline

import (
	"sync"
	"testing"

	"github.com/blinklabs-io/adder/event"
)

// plugin that panics when Stop is called
type panicPlugin struct {
	errChanOnce sync.Once
	errChan     chan error
}

func (p *panicPlugin) Start() error { return nil }
func (p *panicPlugin) Stop() error  { panic("stop panic") }

func (p *panicPlugin) ErrorChan() chan error {
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

func (n *noopPlugin) Start() error { return nil }
func (n *noopPlugin) Stop() error  { return nil }
func (n *noopPlugin) ErrorChan() chan error {
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
