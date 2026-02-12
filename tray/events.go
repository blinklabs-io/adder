// Copyright 2026 Blink Labs Software
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

package tray

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/blinklabs-io/adder/event"
)

const eventChannelBuffer = 64

// EventParser reads newline-delimited JSON events from a reader and
// sends parsed events to a channel.
type EventParser struct {
	scanner *bufio.Scanner
	events  chan event.Event
	done    chan struct{}
}

// NewEventParser creates a new EventParser that reads from r using
// the given buffer size for the scanner.
func NewEventParser(r io.Reader, bufSize int) *EventParser {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, bufSize), bufSize)
	return &EventParser{
		scanner: scanner,
		events:  make(chan event.Event, eventChannelBuffer),
		done:    make(chan struct{}),
	}
}

// Start begins parsing events in a background goroutine. The events
// channel is closed when the reader returns EOF or an error, or when
// Stop is called.
func (ep *EventParser) Start() {
	go ep.run()
}

// Stop signals the event parser to stop. Note that the parser will
// also stop naturally when the underlying reader is closed.
func (ep *EventParser) Stop() {
	select {
	case <-ep.done:
	default:
		close(ep.done)
	}
}

// Events returns a read-only channel of parsed events.
func (ep *EventParser) Events() <-chan event.Event {
	return ep.events
}

func (ep *EventParser) run() {
	defer close(ep.events)

	for ep.scanner.Scan() {
		select {
		case <-ep.done:
			return
		default:
		}

		line := ep.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt event.Event
		if err := json.Unmarshal(line, &evt); err != nil {
			slog.Warn(
				"skipping malformed event line",
				"error", err,
			)
			continue
		}

		select {
		case ep.events <- evt:
		case <-ep.done:
			return
		}
	}

	if err := ep.scanner.Err(); err != nil {
		slog.Debug("event parser scanner error", "error", err)
	}
}
