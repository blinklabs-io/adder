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

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/blinklabs-io/adder/event"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	defaultRingSize  = 100
	clientSendBuffer = 64
)

// EventHub manages event broadcasting to connected WebSocket/SSE clients.
// Call Close() to shut down the hub and release resources.
type EventHub struct {
	mu        sync.RWMutex
	clients   map[*eventClient]struct{}
	ring      []event.Event
	ringPos   int
	ringSize  int
	ringFull  bool
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

type eventClient struct {
	send       chan []byte
	typeFilter map[string]bool // nil means all types
	done       chan struct{}
}

// NewEventHub creates a hub with the given ring buffer size.
func NewEventHub(ringSize uint) *EventHub {
	if ringSize == 0 {
		ringSize = defaultRingSize
	}
	size := int(ringSize)
	return &EventHub{
		clients:  make(map[*eventClient]struct{}),
		ring:     make([]event.Event, size),
		ringSize: size,
		done:     make(chan struct{}),
	}
}

// Broadcast sends an event to all connected clients. Called from
// the pipeline observer goroutine. Non-blocking per client.
//
// The lock is held for the entire client iteration because the
// cleanup path closes client.send under the same lock. Releasing
// the lock before sending would allow a concurrent close, causing
// a send-to-closed-channel panic. The sends are non-blocking
// (select with default) so lock duration is bounded.
func (h *EventHub) Broadcast(evt event.Event) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Debug("failed to marshal event for broadcast", "error", err)
		return
	}

	h.mu.Lock()
	// Store in ring buffer.
	// ringPos is incremented modulo ringSize on each insert. When
	// ringPos wraps to 0, the buffer has been fully written at least
	// once, so ringFull is set permanently. This invariant depends
	// on ringSize being constant after construction.
	h.ring[h.ringPos] = evt
	h.ringPos = (h.ringPos + 1) % h.ringSize
	if h.ringPos == 0 {
		h.ringFull = true
	}

	// Send to all connected clients
	for c := range h.clients {
		if c.typeFilter != nil && !c.typeFilter[evt.Type] {
			continue
		}
		select {
		case c.send <- data:
		default:
			// client too slow, drop event
		}
	}
	h.mu.Unlock()
}

// Close shuts down the hub, terminating the InputChan goroutine and
// allowing client handlers to exit cleanly. Blocks until background
// goroutines have exited.
func (h *EventHub) Close() {
	h.closeOnce.Do(func() {
		close(h.done)
	})
	h.wg.Wait()
}

// InputChan returns a channel that feeds the hub. The hub reads
// events from this channel and broadcasts them. Used as the
// pipeline observer target. The goroutine exits when Close() is
// called or the channel is closed.
func (h *EventHub) InputChan() chan<- event.Event {
	ch := make(chan event.Event, clientSendBuffer)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case evt, ok := <-ch:
				if !ok {
					return
				}
				h.Broadcast(evt)
			case <-h.done:
				return
			}
		}
	}()
	return ch
}

// recentEventsLocked returns buffered events from the ring in order,
// optionally filtered by type. The caller must hold h.mu (read or write).
func (h *EventHub) recentEventsLocked(
	typeFilter map[string]bool,
) []event.Event {
	var events []event.Event
	start := 0
	count := h.ringPos
	if h.ringFull {
		start = h.ringPos
		count = h.ringSize
	}
	for i := range count {
		idx := (start + i) % h.ringSize
		evt := h.ring[idx]
		if evt.Type == "" {
			continue
		}
		if typeFilter != nil && !typeFilter[evt.Type] {
			continue
		}
		events = append(events, evt)
	}
	return events
}

var wsUpgrader = websocket.Upgrader{
	// Allow localhost origins for the tray app. Non-browser clients
	// (no Origin header) are always permitted.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser clients (e.g., tray app)
		}
		return strings.HasPrefix(origin, "http://localhost") ||
			strings.HasPrefix(origin, "http://127.0.0.1") ||
			strings.HasPrefix(origin, "http://[::1]")
	},
}

// HandleEvents is the Gin handler for GET /events. It upgrades to
// WebSocket if possible, otherwise falls back to SSE.
func (h *EventHub) HandleEvents(c *gin.Context) {
	typeFilter := parseTypeFilter(c.Query("types"))

	if websocket.IsWebSocketUpgrade(c.Request) {
		h.handleWebSocket(c.Writer, c.Request, typeFilter)
		return
	}

	h.handleSSE(c, typeFilter)
}

// parseTypeFilter parses a comma-separated list of event types into a
// filter map. Returns nil if no types are specified (meaning all types).
func parseTypeFilter(types string) map[string]bool {
	if types == "" {
		return nil
	}
	filter := make(map[string]bool)
	for _, t := range strings.Split(types, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			filter[t] = true
		}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

func (h *EventHub) handleWebSocket(
	w http.ResponseWriter,
	r *http.Request,
	typeFilter map[string]bool,
) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Debug("websocket upgrade failed", "error", err)
		return
	}

	client := &eventClient{
		send:       make(chan []byte, clientSendBuffer),
		typeFilter: typeFilter,
		done:       make(chan struct{}),
	}

	// Atomically register client and snapshot recent events so no
	// event can be both replayed and delivered via client.send.
	h.mu.Lock()
	h.clients[client] = struct{}{}
	replay := h.recentEventsLocked(typeFilter)
	h.mu.Unlock()

	// Replay recent events directly on this goroutine. This MUST
	// complete before the writer goroutine starts below so that
	// only one goroutine writes to conn at a time.
	for _, evt := range replay {
		data, marshalErr := json.Marshal(evt)
		if marshalErr != nil {
			continue
		}
		if writeErr := conn.WriteMessage(
			websocket.TextMessage,
			data,
		); writeErr != nil {
			break
		}
	}

	// Writer goroutine: reads from client.send and writes to WebSocket
	go func() {
		defer conn.Close()
		for {
			select {
			case msg, ok := <-client.send:
				if !ok {
					return
				}
				if err := conn.WriteMessage(
					websocket.TextMessage,
					msg,
				); err != nil {
					return
				}
			case <-client.done:
				return
			case <-h.done:
				return
			}
		}
	}()

	// Reader goroutine: detects client disconnect
	go func() {
		defer func() {
			close(client.done)
			h.mu.Lock()
			delete(h.clients, client)
			close(client.send)
			h.mu.Unlock()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func (h *EventHub) handleSSE(c *gin.Context, typeFilter map[string]bool) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	client := &eventClient{
		send:       make(chan []byte, clientSendBuffer),
		typeFilter: typeFilter,
		done:       make(chan struct{}),
	}

	// Atomically register client and snapshot recent events so no
	// event can be both replayed and delivered via client.send.
	h.mu.Lock()
	h.clients[client] = struct{}{}
	replay := h.recentEventsLocked(typeFilter)
	h.mu.Unlock()

	defer func() {
		close(client.done)
		h.mu.Lock()
		delete(h.clients, client)
		close(client.send)
		h.mu.Unlock()
	}()

	// Send recent events from ring buffer
	for _, evt := range replay {
		data, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		_, _ = c.Writer.WriteString("data: " + string(data) + "\n\n")
	}
	c.Writer.Flush()

	clientGone := c.Request.Context().Done()
	for {
		select {
		case msg, ok := <-client.send:
			if !ok {
				return
			}
			_, _ = c.Writer.WriteString("data: " + string(msg) + "\n\n")
			c.Writer.Flush()
		case <-clientGone:
			return
		case <-h.done:
			return
		}
	}
}
