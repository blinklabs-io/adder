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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/gorilla/websocket"
)

const (
	eventChanBuffer    = 64
	maxReconnectDelay  = 30 * time.Second
	baseReconnectDelay = 500 * time.Millisecond
)

// EventClient connects to adder's /events WebSocket endpoint and
// delivers parsed events on a channel. It reconnects automatically
// with exponential backoff when the connection drops.
type EventClient struct {
	address    string
	port       uint
	events     chan event.Event
	stopCh     chan struct{}
	status     *StatusTracker
	typeFilter []string
	mu         sync.Mutex
	conn       *websocket.Conn // guarded by mu
	started    bool
	closeOnce  sync.Once
	wg         sync.WaitGroup
}

// EventClientOption is a functional option for EventClient.
type EventClientOption func(*EventClient)

// WithEventTypes sets the event types to subscribe to. The types are
// sent as a query parameter on the WS URL for server-side filtering.
func WithEventTypes(types []string) EventClientOption {
	return func(c *EventClient) {
		c.typeFilter = append([]string(nil), types...)
	}
}

// WithEventStatusTracker sets a StatusTracker that the client updates
// as its connection state changes. A nil tracker is ignored.
func WithEventStatusTracker(t *StatusTracker) EventClientOption {
	return func(c *EventClient) {
		if t != nil {
			c.status = t
		}
	}
}

// NewEventClient creates an EventClient that will connect to the
// given address and port.
func NewEventClient(
	address string,
	port uint,
	opts ...EventClientOption,
) *EventClient {
	c := &EventClient{
		address: address,
		port:    port,
		events:  make(chan event.Event, eventChanBuffer),
		stopCh:  make(chan struct{}),
		status:  NewStatusTracker(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Events returns a read-only channel of events received from the
// server.
func (c *EventClient) Events() <-chan event.Event {
	return c.events
}

// Start begins the background connection loop. Returns an error if
// already started.
func (c *EventClient) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return errors.New("event client already started")
	}
	c.started = true
	c.status.Set(StatusStarting)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.connectLoop()
	}()
	return nil
}

// Stop closes the connection and stops reconnection. It is safe to
// call multiple times or without a prior Start.
func (c *EventClient) Stop() {
	c.mu.Lock()
	wasStarted := c.started
	c.mu.Unlock()

	if !wasStarted {
		return
	}

	c.closeOnce.Do(func() {
		close(c.stopCh)
	})

	// Close the active connection to unblock ReadMessage
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()

	c.wg.Wait()
}

// wsURL builds the WebSocket URL with optional type filter query
// parameter.
func (c *EventClient) wsURL() string {
	u := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("%s:%d", c.address, c.port),
		Path:   "/events",
	}
	if len(c.typeFilter) > 0 {
		q := u.Query()
		q.Set("types", strings.Join(c.typeFilter, ","))
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// connectLoop is the main reconnection loop. It runs in its own
// goroutine and exits when stopCh is closed.
func (c *EventClient) connectLoop() {
	defer func() {
		c.status.Set(StatusStopped)
		close(c.events)
	}()

	attempt := 0
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		conn, err := c.dial()
		if err != nil {
			slog.Debug("ws dial failed", "error", err, "attempt", attempt)
			attempt++
			if !c.backoff(attempt) {
				return
			}
			continue
		}

		// Connected — store conn so Stop() can close it.
		// Re-check stopCh under the same lock to handle the race
		// where Stop() already checked c.conn (was nil during dial).
		c.mu.Lock()
		c.conn = conn
		select {
		case <-c.stopCh:
			c.conn = nil
			c.mu.Unlock()
			conn.Close()
			return
		default:
		}
		c.mu.Unlock()

		attempt = 0
		c.status.Set(StatusConnected)
		slog.Info("connected to adder events endpoint", "url", c.wsURL())

		// Read events until error or stop
		reconnect := c.readLoop(conn)

		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		conn.Close()

		if !reconnect {
			return
		}

		// Connection lost, try to reconnect
		c.status.Set(StatusReconnecting)
		attempt++
		if !c.backoff(attempt) {
			return
		}
	}
}

// dial connects to the WS endpoint.
func (c *EventClient) dial() (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.Dial(c.wsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("dialing ws: %w", err)
	}
	return conn, nil
}

// readLoop reads messages from the WS connection and delivers them
// to the events channel. Returns true if it should reconnect, false
// if stop was requested.
func (c *EventClient) readLoop(conn *websocket.Conn) bool {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Check if we were asked to stop
			select {
			case <-c.stopCh:
				return false
			default:
				slog.Debug("ws read error, will reconnect", "error", err)
				return true
			}
		}

		var evt event.Event
		if err := json.Unmarshal(msg, &evt); err != nil {
			slog.Debug("failed to unmarshal event", "error", err)
			continue
		}

		// Non-blocking send
		select {
		case c.events <- evt:
		default:
			slog.Debug("event dropped, channel full", "type", evt.Type)
		}
	}
}

// backoff waits for an exponential delay before the next reconnect
// attempt. Returns false if stop was requested during the wait.
func (c *EventClient) backoff(attempt int) bool {
	delay := backoffDelay(attempt)
	slog.Debug("reconnect backoff", "delay", delay, "attempt", attempt)

	select {
	case <-time.After(delay):
		return true
	case <-c.stopCh:
		return false
	}
}

// backoffDelay returns an exponential delay capped at
// maxReconnectDelay.
func backoffDelay(attempt int) time.Duration {
	delay := baseReconnectDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > maxReconnectDelay {
			return maxReconnectDelay
		}
	}
	return delay
}
