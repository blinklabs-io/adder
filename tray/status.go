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

// StatusTracker provides thread-safe status tracking with change
// notification. It notifies an optional observer when the status
// changes.
type StatusTracker struct {
	mu       sync.RWMutex
	status   Status
	observer func(Status)
}

// NewStatusTracker creates a StatusTracker with initial status
// StatusStopped.
func NewStatusTracker() *StatusTracker {
	return &StatusTracker{}
}

// Status returns the current status.
func (t *StatusTracker) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// Set updates the status and notifies the observer asynchronously if
// the status changed.
func (t *StatusTracker) Set(s Status) {
	t.mu.Lock()
	if t.status == s {
		t.mu.Unlock()
		return
	}
	t.status = s
	obs := t.observer
	t.mu.Unlock()

	if obs != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("status observer panicked",
						"status", s,
						"panic", r,
					)
				}
			}()
			obs(s)
		}()
	}
}

// OnChange registers a callback that is invoked whenever the status
// changes. Only one observer can be registered at a time.
func (t *StatusTracker) OnChange(fn func(Status)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.observer = fn
}
