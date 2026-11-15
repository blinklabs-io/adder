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

package cardano

import (
	"context"

	"github.com/blinklabs-io/adder/plugin"
)

type Cardano struct {
	plugin.Base
	filterSet
	onDrop         func()
	pipelineOnDrop func()
}

// SetDropObserver records events rejected by this filter. Configure before Start.
func (c *Cardano) SetDropObserver(observer func()) { c.onDrop = observer }

// SetPipelineDropObserver records rejected events for the owning pipeline.
// Configure before Start; replacing the callback keeps restarts idempotent.
func (c *Cardano) SetPipelineDropObserver(
	observer func(),
) {
	c.pipelineOnDrop = observer
}

// New returns a new Cardano object with the specified options applied
func New(options ...CardanoOptionFunc) *Cardano {
	c := &Cardano{}
	for _, option := range options {
		option(c)
	}
	return c
}

// Role identifies this plugin as a pipeline filter.
func (c *Cardano) Role() plugin.PluginType { return plugin.PluginTypeFilter }

// Start the cardano filter
func (c *Cardano) Start() error {
	return c.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (c *Cardano) StartContext(ctx context.Context) error {
	return c.StartRun(ctx,
		plugin.BaseConfig{HasInput: true, HasOutput: true},
		c.start,
		plugin.ShutdownHooks{},
	)
}

func (c *Cardano) start(ctx context.Context) error {
	c.Go(func() { c.processEvents(ctx) })
	return nil
}

// processEvents handles incoming events and applies filters
func (c *Cardano) processEvents(ctx context.Context) {
	done, in := c.Done(), c.Input()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case evt, ok := <-in:
			// Channel closed: we're shutting down
			if !ok {
				return
			}
			if c.filterEvent(evt) {
				if !c.Emit(evt) {
					return
				}
			} else {
				if c.onDrop != nil {
					c.onDrop()
				}
				if c.pipelineOnDrop != nil {
					c.pipelineOnDrop()
				}
			}
		}
	}
}

// Stop the cardano filter
func (c *Cardano) Stop() error {
	return c.Shutdown(plugin.ShutdownHooks{})
}

var _ plugin.ManagedPlugin = (*Cardano)(nil)
