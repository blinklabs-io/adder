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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsUpgrader is a permissive upgrader for test WS servers.
var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// newTestWSServer creates a test WS server that sends events written
// to the returned channel. Close the done channel to shut down.
func newTestWSServer(
	t *testing.T,
) (serverURL string, send chan<- event.Event, done chan struct{}) {
	t.Helper()
	events := make(chan event.Event, 64)
	closeCh := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Reader goroutine to detect disconnect
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()

		for {
			select {
			case evt := <-events:
				data, err := json.Marshal(evt)
				if err != nil {
					continue
				}
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			case <-closeCh:
				return
			}
		}
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server.URL, events, closeCh
}

func TestEventClient_ReceivesEvents(t *testing.T) {
	serverURL, send, done := newTestWSServer(t)
	defer close(done)

	// Parse the server URL to get address and port
	address, port := parseTestURL(t, serverURL)

	client := NewEventClient(address, port)
	require.NoError(t, client.Start())
	defer client.Stop()

	// Wait for connection
	waitForStatus(t, client.status, StatusConnected, 2*time.Second)

	// Send an event from the server
	testEvt := event.Event{
		Type:      "input.block",
		Timestamp: time.Now().Truncate(time.Millisecond),
		Payload:   map[string]string{"hash": "abc123"},
	}
	send <- testEvt

	// Receive the event on the client
	select {
	case received := <-client.Events():
		assert.Equal(t, testEvt.Type, received.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventClient_ReconnectsOnClose(t *testing.T) {
	// Start a server that we can restart
	var mu sync.Mutex
	connCount := 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		connCount++
		count := connCount
		mu.Unlock()

		if count == 1 {
			// First connection: close immediately to trigger reconnect
			conn.Close()
			return
		}

		// Second connection: keep alive
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	address, port := parseTestURL(t, server.URL)

	tracker := NewStatusTracker()
	client := NewEventClient(
		address,
		port,
		WithEventStatusTracker(tracker),
	)
	require.NoError(t, client.Start())
	defer client.Stop()

	// Should eventually reconnect after the first connection drops
	waitForStatus(t, tracker, StatusConnected, 5*time.Second)

	mu.Lock()
	assert.GreaterOrEqual(t, connCount, 2, "should have reconnected at least once")
	mu.Unlock()
}

func TestEventClient_TypeFilter(t *testing.T) {
	// Verify that the type filter is sent as a query parameter
	queryCh := make(chan string, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCh <- r.URL.RawQuery
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	address, port := parseTestURL(t, server.URL)

	client := NewEventClient(
		address,
		port,
		WithEventTypes([]string{"input.block", "input.transaction"}),
	)
	require.NoError(t, client.Start())
	defer client.Stop()

	select {
	case receivedQuery := <-queryCh:
		assert.Contains(t, receivedQuery, "types=")
		assert.Contains(t, receivedQuery, "input.block")
		assert.Contains(t, receivedQuery, "input.transaction")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WS connection")
	}
}

func TestEventClient_StopCleansUp(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	tracker := NewStatusTracker()
	client := NewEventClient(
		address,
		port,
		WithEventStatusTracker(tracker),
	)
	require.NoError(t, client.Start())

	waitForStatus(t, tracker, StatusConnected, 2*time.Second)

	client.Stop()

	assert.Equal(t, StatusStopped, tracker.Status())

	// Events channel should be closed
	select {
	case _, ok := <-client.Events():
		assert.False(t, ok, "events channel should be closed after stop")
	case <-time.After(time.Second):
		t.Fatal("events channel not closed after stop")
	}
}

func TestEventClient_DoubleStartReturnsError(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	client := NewEventClient(address, port)
	require.NoError(t, client.Start())
	defer client.Stop()

	err := client.Start()
	assert.Error(t, err, "second Start should return error")
}

func TestEventClient_StopWithoutStart(t *testing.T) {
	client := NewEventClient("127.0.0.1", 9999)
	// Should not panic
	client.Stop()
}

func TestEventClient_NilStatusTracker(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	// Passing nil tracker should not panic — default is used
	client := NewEventClient(
		address,
		port,
		WithEventStatusTracker(nil),
	)
	require.NoError(t, client.Start())
	defer client.Stop()

	waitForStatus(t, client.status, StatusConnected, 2*time.Second)
}

// parseTestURL extracts the host and port from an httptest server URL.
func parseTestURL(t *testing.T, rawURL string) (string, uint) {
	t.Helper()
	// URL is like "http://127.0.0.1:PORT"
	trimmed := strings.TrimPrefix(rawURL, "http://")
	parts := strings.Split(trimmed, ":")
	require.Len(t, parts, 2)
	var port uint
	_, err := fmt.Sscanf(parts[1], "%d", &port)
	require.NoError(t, err)
	return parts[0], port
}

// waitForStatus polls the tracker until it reaches the expected status
// or the timeout expires.
func waitForStatus(
	t *testing.T,
	tracker *StatusTracker,
	expected Status,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tracker.Status() == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"status did not reach %s within %s (current: %s)",
		expected, timeout, tracker.Status(),
	)
}
