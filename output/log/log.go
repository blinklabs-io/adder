package log

import (
	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/internal/logging"
)

type LogOutput struct {
	errorChan chan error
	eventChan chan event.Event
	logger    *logging.Logger
	level     string
}

func New(options ...LogOptionFunc) *LogOutput {
	l := &LogOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
		logger:    logging.GetLogger().With("type", "event"),
		level:     "info",
	}
	for _, option := range options {
		option(l)
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
			switch l.level {
			case "info":
				l.logger.Infow("", "event", evt)
			case "warn":
				l.logger.Warnw("", "event", evt)
			case "error":
				l.logger.Errorw("", "event", evt)
			default:
				// Use INFO level if log level isn't recognized
				l.logger.Infow("", "event", evt)
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
