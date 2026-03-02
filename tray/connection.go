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
	return cm.connectLocked()
}

// connectLocked is the implementation of Connect. The caller must
// hold cm.mu.
func (cm *ConnectionManager) connectLocked() error {
	if cm.connected {
		return errors.New("already connected")
	}

	client := NewEventClient(
		cm.apiAddress,
		cm.apiPort,
		WithEventStatusTracker(cm.status),
	)

	if err := client.Start(); err != nil {
		return err
	}

	cm.eventClient = client
	cm.connected = true

	// Forward events from this specific client to our channel. Binding
	// the client at spawn time prevents quick reconnects from spawning
	// multiple forwarders that race on a stale cm.eventClient read.
	go cm.forwardEvents(client)

	return nil
}

// Disconnect stops the EventClient and cleans up.
func (cm *ConnectionManager) Disconnect() {
	cm.mu.Lock()
	client := cm.disconnectLocked()
	cm.mu.Unlock()

	if client != nil {
		client.Stop()
	}
}

// disconnectLocked clears connection state under the caller's lock
// and returns the EventClient that should be Stopped, or nil if no
// connection was active. Stop() is deliberately left to the caller so
// it can decide whether to release cm.mu first (Disconnect does) or
// hold it across the Stop call (Reconnect does, for atomicity). It
// is safe to call Stop() while holding cm.mu: none of the EventClient
// goroutines (connectLoop, readLoop, backoff) or forwardEvents
// acquire cm.mu, so there is no lock-order hazard.
func (cm *ConnectionManager) disconnectLocked() *EventClient {
	if !cm.connected {
		return nil
	}
	client := cm.eventClient
	cm.connected = false
	cm.eventClient = nil
	return client
}

// Reconnect atomically disconnects and reconnects under a single
// lock acquisition, so concurrent Connect/Disconnect/Reconnect
// callers cannot interleave and leave the manager in an inconsistent
// state (e.g., Reconnect reporting "already connected" because
// another goroutine raced in between the inner Disconnect and
// Connect).
func (cm *ConnectionManager) Reconnect() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client := cm.disconnectLocked(); client != nil {
		client.Stop()
	}
	return cm.connectLocked()
}

// Events returns a read-only channel of events received from adder.
func (cm *ConnectionManager) Events() <-chan event.Event {
	return cm.events
}

// IsConnected reports whether the EventClient is actively connected.
func (cm *ConnectionManager) IsConnected() bool {
	return cm.status.Status() == StatusConnected
}

// forwardEvents reads from the given EventClient's events channel and
// forwards them to the ConnectionManager's events channel. Runs until
// the EventClient's channel is closed. The client is passed as a
// parameter (rather than read from cm.eventClient) so that each
// forwarder is bound to the client it was spawned for.
func (cm *ConnectionManager) forwardEvents(client *EventClient) {
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
