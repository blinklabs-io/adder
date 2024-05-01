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

package log

import (
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

type LogOutput struct {
	errorChan    chan error
	eventChan    chan event.Event
	logger       plugin.Logger
	outputLogger *logging.Logger
	level        string
}

func New(options ...LogOptionFunc) *LogOutput {
	l := &LogOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
		level:     "info",
	}
	for _, option := range options {
		option(l)
	}
	if l.logger == nil {
		l.logger = logging.GetLogger()
	}
	// Determine if we can use the provided logger or need our own
	// This is necessary because this plugin uses logger functions that aren't part
	// of the plugin.Logger interface
	switch v := l.logger.(type) {
	case *logging.Logger:
		l.outputLogger = v
	default:
		l.outputLogger = logging.GetLogger()
	}
	l.outputLogger = l.outputLogger.With("type", "event")
	return l
}

// Start the log output
func (l *LogOutput) Start() error {
	go func() {
		for {
			evt, ok := <-l.eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			switch l.level {
			case "info":
				l.outputLogger.Infow("", "event", evt)
			case "warn":
				l.outputLogger.Warnw("", "event", evt)
			case "error":
				l.outputLogger.Errorw("", "event", evt)
			default:
				// Use INFO level if log level isn't recognized
				l.outputLogger.Infow("", "event", evt)
			}
		}
	}()
	return nil
}

// Stop the log output
func (l *LogOutput) Stop() error {
	close(l.eventChan)
	close(l.errorChan)
	return nil
}

// ErrorChan returns the input error channel
func (l *LogOutput) ErrorChan() chan error {
	return l.errorChan
}

// InputChan returns the input event channel
func (l *LogOutput) InputChan() chan<- event.Event {
	return l.eventChan
}

// OutputChan always returns nil
func (l *LogOutput) OutputChan() <-chan event.Event {
	return nil
}
