package pipeline

import (
	"testing"

	"github.com/blinklabs-io/adder/event"
)

// plugin that panics when Stop is called
type panicPlugin struct{}

func (p *panicPlugin) Start() error { return nil }
func (p *panicPlugin) Stop() error  { panic("stop panic") }

func (p *panicPlugin) SetErrorChan(chan<- error)      {}
func (p *panicPlugin) InputChan() chan<- event.Event  { return nil }
func (p *panicPlugin) OutputChan() <-chan event.Event { return nil }

// simple no-op plugin
type noopPlugin struct{}

func (n *noopPlugin) Start() error                   { return nil }
func (n *noopPlugin) Stop() error                    { return nil }
func (n *noopPlugin) SetErrorChan(chan<- error)      {}
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
