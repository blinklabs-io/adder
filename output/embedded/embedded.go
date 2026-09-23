// Copyright 2023 Blink Labs Software
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

package embedded

import (
	"context"
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

// ErrForwardChanClosed is returned by Start when the plugin was given a
// channel with WithOutputChan and has already been stopped. Stop closes
// that channel, so the forwarding path cannot come back up. Build a new
// plugin around a new channel instead.
var ErrForwardChanClosed = errors.New(
	"embedded: forward channel was closed by Stop, so this plugin cannot " +
		"be restarted; construct a new one with a new channel",
)

// CallbackFunc receives immutable events. It must return for Stop to complete.
// Prefer ContextCallbackFunc for operations that can block.
type CallbackFunc func(event.Event) error

// ContextCallbackFunc receives the run context and an immutable event. It must
// return when the context is canceled. Returning an error fails the plugin.
type ContextCallbackFunc func(context.Context, event.Event) error

type EmbeddedOutput struct {
	plugin.Base
	callbackFunc        CallbackFunc
	contextCallbackFunc ContextCallbackFunc
	// forwardChan is a caller-supplied channel that receives a copy of
	// every event. It is not the pipeline output channel: OutputChan
	// stays nil, because this plugin is a sink from the pipeline's point
	// of view.
	forwardChan chan event.Event
	// forwardClosed records that Stop closed forwardChan. This plugin is
	// the only sender on it, so closing is the sender's job and is what
	// lets a caller ranging over the channel terminate. The cost is that
	// the forwarding path is single-use, and a restart would otherwise
	// come back up quietly forwarding nothing. Start refuses instead.
	forwardClosed bool
}

func New(options ...EmbeddedOptionFunc) *EmbeddedOutput {
	e := &EmbeddedOutput{}
	for _, option := range options {
		option(e)
	}
	return e
}

// Role identifies this plugin as a pipeline output.
func (e *EmbeddedOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start the embedded output
func (e *EmbeddedOutput) Start() error {
	return e.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (e *EmbeddedOutput) StartContext(ctx context.Context) error {
	return e.StartRun(ctx,
		plugin.BaseConfig{HasInput: true},
		e.start,
		e.shutdownHooks(),
	)
}

func (e *EmbeddedOutput) start(ctx context.Context) error {
	if e.forwardClosed {
		return ErrForwardChanClosed
	}
	// Capture the channel for the worker, so the goroutine never reads
	// the field that Stop writes.
	forward := e.forwardChan

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
				if e.contextCallbackFunc != nil || e.callbackFunc != nil {
					var err error
					if e.contextCallbackFunc != nil {
						err = e.contextCallbackFunc(ctx, evt)
					} else {
						err = e.callbackFunc(evt)
					}
					if err != nil {
						e.Fail(fmt.Errorf(
							"callback function error: %w", err,
						))
						return
					}
				}
				if forward != nil {
					select {
					case <-done:
						return
					case forward <- evt:
					}
				}
			}
		}
	})
	return nil
}

// Stop the embedded output.
//
// It closes the channel supplied with WithOutputChan, because this plugin
// is the only sender on it and a caller ranging over it has nothing else
// to end the range. That makes the forwarding path single-use: a later
// Start returns ErrForwardChanClosed rather than running on with nowhere
// to forward to.
func (e *EmbeddedOutput) Stop() error {
	return e.Shutdown(e.shutdownHooks())
}

func (e *EmbeddedOutput) shutdownHooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{
		AfterWait: func() error {
			if e.forwardChan != nil {
				close(e.forwardChan)
				e.forwardChan = nil
				e.forwardClosed = true
			}
			return nil
		},
	}
}

var _ plugin.ManagedPlugin = (*EmbeddedOutput)(nil)
