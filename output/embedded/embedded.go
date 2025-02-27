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
	"fmt"

	"github.com/blinklabs-io/adder/event"
)

type CallbackFunc func(event.Event) error

type EmbeddedOutput struct {
	errorChan    chan error
	eventChan    chan event.Event
	callbackFunc CallbackFunc
	outputChan   chan event.Event
}

func New(options ...EmbeddedOptionFunc) *EmbeddedOutput {
	e := &EmbeddedOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
	}
	for _, option := range options {
		option(e)
	}
	return e
}

// Start the embedded output
func (e *EmbeddedOutput) Start() error {
	go func() {
		for {
			evt, ok := <-e.eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			if e.callbackFunc != nil {
				if err := e.callbackFunc(evt); err != nil {
					e.errorChan <- fmt.Errorf("callback function error: %w", err)
					return
				}
			}
			if e.outputChan != nil {
				e.outputChan <- evt
			}
		}
	}()
	return nil
}

// Stop the embedded output
func (e *EmbeddedOutput) Stop() error {
	close(e.eventChan)
	close(e.errorChan)
	if e.outputChan != nil {
		close(e.outputChan)
	}
	return nil
}

// ErrorChan returns the input error channel
func (e *EmbeddedOutput) ErrorChan() chan error {
	return e.errorChan
}

// InputChan returns the input event channel
func (e *EmbeddedOutput) InputChan() chan<- event.Event {
	return e.eventChan
}

// OutputChan always returns nil
func (e *EmbeddedOutput) OutputChan() <-chan event.Event {
	return nil
}
