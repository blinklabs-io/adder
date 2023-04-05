package log

import (
	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/internal/logging"
)

type LogOutput struct {
	errorChan chan error
	eventChan chan event.Event
	logger    *logging.Logger
}

func New() *LogOutput {
	l := &LogOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
		logger:    logging.GetLogger().With("type", "event"),
	}
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
			l.logger.Infow("", "event", evt)
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

// EventChan returns the input event channel
func (l *LogOutput) EventChan() chan event.Event {
	return l.eventChan
}
