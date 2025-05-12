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
	"slices"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

type Event struct {
	errorChan   chan error
	inputChan   chan event.Event
	outputChan  chan event.Event
	logger      plugin.Logger
	filterTypes []string
}

// New returns a new Event object with the specified options applied
func New(options ...EventOptionFunc) *Event {
	e := &Event{
		errorChan:  make(chan error),
		inputChan:  make(chan event.Event, 10),
		outputChan: make(chan event.Event, 10),
	}
	for _, option := range options {
		option(e)
	}
	return e
}

// Start the event filter
func (e *Event) Start() error {
	go func() {
		for {
			evt, ok := <-e.inputChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			// Drop events if we have a type filter configured and the event doesn't match
			if len(e.filterTypes) > 0 {
				matched := slices.Contains(e.filterTypes, evt.Type)
				if !matched {
					continue
				}
			}
			// Send event along
			e.outputChan <- evt
		}
	}()
	return nil
}

// Stop the event filter
func (e *Event) Stop() error {
	close(e.inputChan)
	close(e.outputChan)
	close(e.errorChan)
	return nil
}

// ErrorChan returns the filter error channel
func (e *Event) ErrorChan() chan error {
	return e.errorChan
}

// InputChan returns the input event channel
func (e *Event) InputChan() chan<- event.Event {
	return e.inputChan
}

// OutputChan returns the output event channel
func (e *Event) OutputChan() <-chan event.Event {
	return e.outputChan
}
