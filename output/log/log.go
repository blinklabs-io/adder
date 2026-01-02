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
	"fmt"
	"log/slog"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

type LogOutput struct {
	errorChan    chan error
	eventChan    chan event.Event
	logger       plugin.Logger
	outputLogger *slog.Logger
	level        string
}

func New(options ...LogOptionFunc) *LogOutput {
	l := &LogOutput{
		level: "info",
	}
	for _, option := range options {
		option(l)
	}
	if l.logger == nil {
		l.logger = logging.GetLogger()
	}

	// Use the provided *slog.Logger if available, otherwise fall back to global logger
	if providedLogger, ok := l.logger.(*slog.Logger); ok {
		l.outputLogger = providedLogger.With("type", "event")
	} else {
		l.outputLogger = logging.GetLogger().With("type", "event")
	}
	return l
}

// Start the log output
func (l *LogOutput) Start() error {
	l.eventChan = make(chan event.Event, 10)
	l.errorChan = make(chan error)
	go func() {
		for {
			evt, ok := <-l.eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			switch l.level {
			case "info":
				l.outputLogger.Info("", "event", fmt.Sprintf("%+v", evt))
			case "warn":
				l.outputLogger.Warn("", "event", fmt.Sprintf("%+v", evt))
			case "error":
				l.outputLogger.Error("", "event", fmt.Sprintf("%+v", evt))
			default:
				// Use INFO level if log level isn't recognized
				l.outputLogger.Info("", "event", fmt.Sprintf("%+v", evt))
			}
		}
	}()
	return nil
}

// Stop the log output
func (l *LogOutput) Stop() error {
	if l.eventChan != nil {
		close(l.eventChan)
		l.eventChan = nil
	}
	if l.errorChan != nil {
		close(l.errorChan)
		l.errorChan = nil
	}
	return nil
}

// ErrorChan returns the plugin's error channel
func (l *LogOutput) ErrorChan() <-chan error {
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
