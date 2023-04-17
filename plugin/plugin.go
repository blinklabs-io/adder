package plugin

import (
	"github.com/blinklabs-io/snek/event"
)

type Plugin interface {
	Start() error
	Stop() error
	ErrorChan() chan error
	InputChan() chan<- event.Event
	OutputChan() <-chan event.Event
}
