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
	"errors"
	"log/slog"
	"sync"

	"github.com/blinklabs-io/adder/event"
)

// ConnectionManager manages the EventClient WS connection to adder's
// API. It replaces the old ProcessManager — the tray no longer
// manages adder as a subprocess but connects to it as an API client.
type ConnectionManager struct {
	mu          sync.Mutex
	apiAddress  string
	apiPort     uint
	status      *StatusTracker
	eventClient *EventClient
	events      chan event.Event
	connected   bool
}

// ConnectionOption is a functional option for ConnectionManager.
type ConnectionOption func(*ConnectionManager)

// WithConnectionAddress sets the adder API address.
func WithConnectionAddress(address string) ConnectionOption {
	return func(cm *ConnectionManager) {
		cm.apiAddress = address
	}
}

// WithConnectionPort sets the adder API port.
func WithConnectionPort(port uint) ConnectionOption {
	return func(cm *ConnectionManager) {
		cm.apiPort = port
	}
}

// WithConnectionStatusTracker sets a StatusTracker that the manager
// updates as the connection state changes.
func WithConnectionStatusTracker(t *StatusTracker) ConnectionOption {
	return func(cm *ConnectionManager) {
		if t != nil {
			cm.status = t
		}
	}
}

// NewConnectionManager creates a ConnectionManager with the given
// options.
func NewConnectionManager(opts ...ConnectionOption) *ConnectionManager {
	cm := &ConnectionManager{
		apiAddress: "127.0.0.1",
		apiPort:    8080,
		status:     NewStatusTracker(),
		events:     make(chan event.Event, eventChanBuffer),
	}
	for _, opt := range opts {
		opt(cm)
	}
	return cm
}

// Connect creates and starts an EventClient that connects to adder's
// /events WebSocket endpoint. Returns an error if already connected.
func (cm *ConnectionManager) Connect() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.connected {
		return errors.New("already connected")
	}

	cm.eventClient = NewEventClient(
		cm.apiAddress,
		cm.apiPort,
		WithEventStatusTracker(cm.status),
	)

	if err := cm.eventClient.Start(); err != nil {
		return err
	}

	cm.connected = true

	// Forward events from EventClient to our channel
	go cm.forwardEvents()

	return nil
}

// Disconnect stops the EventClient and cleans up.
func (cm *ConnectionManager) Disconnect() {
	cm.mu.Lock()
	if !cm.connected {
		cm.mu.Unlock()
		return
	}
	client := cm.eventClient
	cm.connected = false
	cm.eventClient = nil
	cm.mu.Unlock()

	client.Stop()
}

// Reconnect disconnects and then reconnects.
func (cm *ConnectionManager) Reconnect() error {
	cm.Disconnect()
	return cm.Connect()
}

// Events returns a read-only channel of events received from adder.
func (cm *ConnectionManager) Events() <-chan event.Event {
	return cm.events
}

// IsConnected reports whether the EventClient is actively connected.
func (cm *ConnectionManager) IsConnected() bool {
	return cm.status.Status() == StatusConnected
}

// forwardEvents reads from the EventClient's events channel and
// forwards them to the ConnectionManager's events channel. Runs
// until the EventClient's channel is closed.
func (cm *ConnectionManager) forwardEvents() {
	cm.mu.Lock()
	client := cm.eventClient
	cm.mu.Unlock()

	if client == nil {
		return
	}

	for evt := range client.Events() {
		select {
		case cm.events <- evt:
		default:
			slog.Debug("event dropped, forwarding channel full",
				"type", evt.Type,
			)
		}
	}
}
