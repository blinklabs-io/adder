// Copyright 2025 Blink Labs Software
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

package event

import (
	"context"
	"slices"

	"github.com/blinklabs-io/adder/plugin"
)

type Event struct {
	plugin.Base
	filterTypes []string
}

// New returns a new Event object with the specified options applied
func New(options ...EventOptionFunc) *Event {
	e := &Event{}
	for _, option := range options {
		option(e)
	}
	return e
}

// Role identifies this plugin as a pipeline filter.
func (e *Event) Role() plugin.PluginType { return plugin.PluginTypeFilter }

// Start the event filter
func (e *Event) Start() error {
	return e.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (e *Event) StartContext(ctx context.Context) error {
	return e.StartRun(ctx,
		plugin.BaseConfig{HasInput: true, HasOutput: true},
		e.start,
		plugin.ShutdownHooks{},
	)
}

func (e *Event) start(ctx context.Context) error {
	done, in := e.Done(), e.Input()
	e.Go(func() {
		for {
			select {
			case <-done:
				return
			case evt, ok := <-in:
				// Channel closed: we're shutting down
				if !ok {
					return
				}
				// Drop events if we have a type filter configured and
				// the event doesn't match
				if len(e.filterTypes) > 0 &&
					!slices.Contains(e.filterTypes, evt.Type) {
					continue
				}
				if !e.Emit(evt) {
					return
				}
			}
		}
	})
	return nil
}

// Stop the event filter
func (e *Event) Stop() error {
	return e.Shutdown(plugin.ShutdownHooks{})
}

var _ plugin.ManagedPlugin = (*Event)(nil)
