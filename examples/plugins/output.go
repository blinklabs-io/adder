// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"
	"errors"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

// HandlerOutput passes immutable events to a cancellation-aware handler.
// A handler error is terminal. The handler must return on context cancellation.
type HandlerOutput struct {
	plugin.Base
	handle func(context.Context, event.Event) error
}

var _ plugin.ManagedPlugin = (*HandlerOutput)(nil)

// NewHandlerOutput validates its dependency without acquiring resources.
func NewHandlerOutput(
	handle func(context.Context, event.Event) error,
) (*HandlerOutput, error) {
	if handle == nil {
		return nil, errors.New("event handler is required")
	}
	return &HandlerOutput{handle: handle}, nil
}

// Role declares this plugin's event ports.
func (*HandlerOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start starts an independently owned run. Pipelines use StartContext.
func (p *HandlerOutput) Start() error { return p.StartContext(context.Background()) }

// StartContext starts the handler worker with the run's context.
func (p *HandlerOutput) StartContext(ctx context.Context) error {
	return p.StartRun(
		ctx,
		plugin.BaseConfig{HasInput: true},
		func(ctx context.Context) error {
			in := p.Input()
			p.Go(func() {
				for {
					select {
					case <-ctx.Done():
						return
					case evt, ok := <-in:
						if !ok {
							return
						}
						if err := p.handle(ctx, evt); err != nil {
							p.Fail(err)
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

// Stop cancels the handler and joins it. Cancellation must be cooperative.
func (p *HandlerOutput) Stop() error { return p.Shutdown(plugin.ShutdownHooks{}) }
