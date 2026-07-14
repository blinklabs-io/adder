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
	"log/slog"
	"sync"
)

// Status represents the connection status of the tray application.
type Status int

const (
	StatusStopped      Status = iota
	StatusStarting            // connecting to WS endpoint
	StatusConnected           // WS connection established
	StatusReconnecting        // WS connection lost, retrying
	StatusError               // unrecoverable error
)

// String returns a human-readable status label.
func (s Status) String() string {
	switch s {
	case StatusStopped:
		return "stopped"
	case StatusStarting:
		return "starting"
	case StatusConnected:
		return "connected"
	case StatusReconnecting:
		return "reconnecting"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

// statusQueueSize bounds the pending-transition buffer. Transitions are
// infrequent (a handful per apply/reconnect) but the observer can sleep
// between deliveries, so buffer generously to keep Set non-blocking.
const statusQueueSize = 64

// StatusTracker provides thread-safe status tracking with change
// notification. It notifies an optional observer when the status
// changes. Transitions are delivered to the observer by a single
// long-lived worker goroutine, so the observer always sees them in the
// order they occurred (rapid flips can no longer race).
type StatusTracker struct {
	mu       sync.RWMutex
	status   Status
	observer func(Status)
	queue    chan Status
}

// NewStatusTracker creates a StatusTracker with initial status
// StatusStopped and starts its ordered-delivery worker.
func NewStatusTracker() *StatusTracker {
	t := &StatusTracker{queue: make(chan Status, statusQueueSize)}
	go t.deliverLoop()
	return t
}

// deliverLoop drains queued transitions in order, invoking the current
// observer for each. One goroutine owns delivery, so observer calls are
// serialized and ordered.
func (t *StatusTracker) deliverLoop() {
	for s := range t.queue {
		t.mu.RLock()
		obs := t.observer
		t.mu.RUnlock()
		if obs != nil {
			t.invoke(obs, s)
		}
	}
}

// invoke calls the observer with panic recovery so a misbehaving
// observer cannot kill the delivery worker.
func (t *StatusTracker) invoke(obs func(Status), s Status) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("status observer panicked",
				"status", s,
				"panic", r,
			)
		}
	}()
	obs(s)
}

// Status returns the current status.
func (t *StatusTracker) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// Set updates the status and enqueues an ordered observer notification
// if the status changed.
func (t *StatusTracker) Set(s Status) {
	t.mu.Lock()
	if t.status == s {
		t.mu.Unlock()
		return
	}
	t.status = s
	t.mu.Unlock()

	// Enqueue for ordered delivery. Buffered so this stays non-blocking
	// for realistic transition rates.
	t.queue <- s
}

// OnChange registers a callback that is invoked whenever the status
// changes. It is immediately called once with the current status
// synchronously. Only one observer can be registered at a time.
func (t *StatusTracker) OnChange(fn func(Status)) {
	t.mu.Lock()
	t.observer = fn
	s := t.status
	t.mu.Unlock()

	if fn != nil {
		t.invoke(fn, s)
	}
}
