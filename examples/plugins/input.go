// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

// Package plugins contains minimal managed plugins for authors to adapt.
package plugins

import (
	"context"
	"errors"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

// TickerInput publishes example.tick events until its run is canceled.
type TickerInput struct {
	plugin.Base
	interval time.Duration
}

var _ plugin.ManagedPlugin = (*TickerInput)(nil)

// NewTickerInput validates its configuration without acquiring resources.
func NewTickerInput(interval time.Duration) (*TickerInput, error) {
	if interval <= 0 {
		return nil, errors.New("tick interval must be positive")
	}
	return &TickerInput{interval: interval}, nil
}

// Role declares this plugin's event ports.
func (*TickerInput) Role() plugin.PluginType { return plugin.PluginTypeInput }

// Start starts an independently owned run. Pipelines use StartContext.
func (p *TickerInput) Start() error { return p.StartContext(context.Background()) }

// StartContext starts production with cancellation controlled by ctx.
func (p *TickerInput) StartContext(ctx context.Context) error {
	return p.StartRun(
		ctx,
		plugin.BaseConfig{HasOutput: true},
		func(ctx context.Context) error {
			p.Go(func() {
				ticker := time.NewTicker(p.interval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case now := <-ticker.C:
						if !p.Emit(
							event.New("example.tick", now, nil, now.UnixNano()),
						) {
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

// Stop cancels production and joins the worker before closing its channels.
func (p *TickerInput) Stop() error { return p.Shutdown(plugin.ShutdownHooks{}) }
