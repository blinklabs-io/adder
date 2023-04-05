package input

import (
	"github.com/blinklabs-io/snek/event"
)

type Input interface {
	Start() error
	Stop() error
	ErrorChan() chan error
	EventChan() chan event.Event
}
