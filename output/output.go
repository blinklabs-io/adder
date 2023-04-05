package output

import (
	"github.com/blinklabs-io/snek/event"
)

type Output interface {
	Start() error
	Stop() error
	ErrorChan() chan error
	EventChan() chan event.Event
}
