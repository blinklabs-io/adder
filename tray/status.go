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

import "sync"

// Status represents the current state of the adder process.
type Status string

const (
	StatusStopped      Status = "stopped"
	StatusStarting     Status = "starting"
	StatusConnected    Status = "connected"
	StatusReconnecting Status = "reconnecting"
	StatusError        Status = "error"
)

// String returns a human-readable label for the status.
func (s Status) String() string {
	switch s {
	case StatusStopped:
		return "Stopped"
	case StatusStarting:
		return "Starting"
	case StatusConnected:
		return "Connected"
	case StatusReconnecting:
		return "Reconnecting"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}

// StatusTracker provides thread-safe tracking of the adder process
// status with an optional change observer callback.
type StatusTracker struct {
	mu       sync.Mutex
	current  Status
	onChange func(Status)
}

// NewStatusTracker creates a new StatusTracker with an initial status
// of StatusStopped.
func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		current: StatusStopped,
	}
}

// Get returns the current status.
func (st *StatusTracker) Get() Status {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.current
}

// Set updates the current status. If an onChange callback is
// registered and the status has changed, the callback is invoked
// after the lock is released with the new status.
func (st *StatusTracker) Set(s Status) {
	st.mu.Lock()
	if st.current == s {
		st.mu.Unlock()
		return
	}
	st.current = s
	fn := st.onChange
	st.mu.Unlock()
	if fn != nil {
		fn(s)
	}
}

// OnChange registers a callback that is called whenever the status
// changes. Only one callback can be registered at a time; subsequent
// calls replace the previous callback.
func (st *StatusTracker) OnChange(fn func(Status)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.onChange = fn
}
