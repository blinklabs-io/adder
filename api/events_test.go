// Copyright 2025 Blink Labs Software
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

package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/api"
	"github.com/blinklabs-io/adder/event"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestRouter creates a fresh gin engine with the /events route for testing.
// This avoids the singleton API instance used in production.
func newTestRouter(hub *api.EventHub) *gin.Engine {
	g := gin.New()
	g.GET("/events", hub.HandleEvents)
	return g
}

func TestEventHub_WebSocketConnectReceive(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	// Connect via WebSocket
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Broadcast an event
	testEvt := event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
		Payload:   map[string]string{"hash": "abc123"},
	}
	hub.Broadcast(testEvt)

	// Read the event from WebSocket
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var received event.Event
	err = json.Unmarshal(msg, &received)
	require.NoError(t, err)
	assert.Equal(t, testEvt.Type, received.Type)
}

func TestEventHub_RingBufferReplay(t *testing.T) {
	hub := api.NewEventHub(5)
	defer hub.Close()

	// Broadcast some events before any client connects
	for i := 0; i < 3; i++ {
		hub.Broadcast(event.Event{
			Type:      "input.block",
			Timestamp: time.Now(),
			Payload:   map[string]int{"index": i},
		})
	}

	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	// Connect via WebSocket -- should receive recent events
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Read the 3 replayed events
	var received []event.Event
	for i := 0; i < 3; i++ {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, readErr := conn.ReadMessage()
		require.NoError(t, readErr)

		var evt event.Event
		err = json.Unmarshal(msg, &evt)
		require.NoError(t, err)
		received = append(received, evt)
	}

	assert.Len(t, received, 3)
	for _, evt := range received {
		assert.Equal(t, "input.block", evt.Type)
	}
}

func TestEventHub_TypeFiltering(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	// Connect with type filter
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/events?types=input.block"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Broadcast events of different types
	hub.Broadcast(event.Event{
		Type:      "input.transaction",
		Timestamp: time.Now(),
	})
	hub.Broadcast(event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
		Payload:   "wanted",
	})
	hub.Broadcast(event.Event{
		Type:      "input.transaction",
		Timestamp: time.Now(),
	})

	// Should only receive the block event
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var received event.Event
	err = json.Unmarshal(msg, &received)
	require.NoError(t, err)
	assert.Equal(t, "input.block", received.Type)

	// Next read should timeout since we filtered out the transaction events
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "should timeout since no more matching events")
}

func TestEventHub_MultipleClients(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"

	// Connect two clients
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn1.Close()

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn2.Close()

	// Broadcast an event
	hub.Broadcast(event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
	})

	// Both clients should receive it
	for _, conn := range []*websocket.Conn{conn1, conn2} {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, readErr := conn.ReadMessage()
		require.NoError(t, readErr)

		var received event.Event
		err = json.Unmarshal(msg, &received)
		require.NoError(t, err)
		assert.Equal(t, "input.block", received.Type)
	}
}

func TestEventHub_ClientDisconnectCleanup(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	// Close the connection
	conn.Close()

	// Give the hub time to clean up
	time.Sleep(100 * time.Millisecond)

	// Broadcast should not panic with no clients
	hub.Broadcast(event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
	})
}

func TestEventHub_SSEFallback(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	// Pre-broadcast an event so the SSE response has data immediately
	hub.Broadcast(event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
		Payload:   "sse-test",
	})

	// Make a regular HTTP request with timeout so test can't hang
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(
		t,
		"text/event-stream",
		resp.Header.Get("Content-Type"),
	)

	// Read the first SSE line
	scanner := bufio.NewScanner(resp.Body)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			var evt event.Event
			err = json.Unmarshal([]byte(jsonData), &evt)
			require.NoError(t, err)
			assert.Equal(t, "input.block", evt.Type)
			found = true
			break
		}
	}
	assert.True(t, found, "should have received an SSE data line")
}

func TestEventHub_NonBlockingBroadcast(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Broadcast many events rapidly -- should not block even if client
	// is slow
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			hub.Broadcast(event.Event{
				Type:      "input.block",
				Timestamp: time.Now(),
				Payload:   i,
			})
		}
	}()

	// The broadcast goroutine should complete quickly without blocking
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success - broadcasts completed without blocking
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast blocked -- non-blocking send is not working")
	}
}

func TestEventHub_InputChan(t *testing.T) {
	hub := api.NewEventHub(10)
	defer hub.Close()
	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Use InputChan to feed the hub (simulates pipeline observer)
	ch := hub.InputChan()
	ch <- event.Event{
		Type:      "input.transaction",
		Timestamp: time.Now(),
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var received event.Event
	err = json.Unmarshal(msg, &received)
	require.NoError(t, err)
	assert.Equal(t, "input.transaction", received.Type)
}

func TestEventHub_RingBufferWraparound(t *testing.T) {
	hub := api.NewEventHub(3) // small ring
	defer hub.Close()

	// Broadcast 5 events -- ring wraps around
	for i := 0; i < 5; i++ {
		hub.Broadcast(event.Event{
			Type:      "input.block",
			Timestamp: time.Now(),
			Payload:   i,
		})
	}

	router := newTestRouter(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	// Connect and should only see last 3 events
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	var received []event.Event
	for i := 0; i < 3; i++ {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, readErr := conn.ReadMessage()
		require.NoError(t, readErr)

		var evt event.Event
		err = json.Unmarshal(msg, &evt)
		require.NoError(t, err)
		received = append(received, evt)
	}

	assert.Len(t, received, 3)

	// The payloads should be 2, 3, 4 (last 3 of the 5 events)
	for i, evt := range received {
		payload, ok := evt.Payload.(float64) // JSON numbers decode as float64
		require.True(t, ok, "payload should be a number")
		assert.Equal(t, float64(i+2), payload)
	}
}
