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

package api

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthcheckEndpoint(t *testing.T) {
	apiInstance := New(true)

	req, err := http.NewRequest(http.MethodGet, "/healthcheck", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Expected status OK")

	var response map[string]any
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	failed, exists := response["failed"]
	assert.True(t, exists, "Response should contain 'failed' field")
	assert.Equal(t, false, failed, "Expected 'failed' to be false")
}

func TestPingEndpoint(t *testing.T) {
	apiInstance := New(true)

	req, err := http.NewRequest(http.MethodGet, "/ping", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Expected status OK")
	assert.Equal(t, "pong", rr.Body.String(), "Expected 'pong' response")
}

type mockHealthChecker struct {
	running bool
}

func (m *mockHealthChecker) IsRunning() bool {
	return m.running
}

func TestHealthcheckWithUnhealthyPipeline(t *testing.T) {
	apiInstance := New(true)

	t.Cleanup(ResetHealthCheckers)

	mock := &mockHealthChecker{running: false}
	RegisterHealthChecker(mock)

	req, err := http.NewRequest(http.MethodGet, "/healthcheck", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code, "Expected status Service Unavailable")

	var response map[string]any
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	failed, exists := response["failed"]
	assert.True(t, exists, "Response should contain 'failed' field")
	assert.Equal(t, true, failed, "Expected 'failed' to be true")

	reason, exists := response["reason"]
	assert.True(t, exists, "Response should contain 'reason' field")
	assert.Equal(t, "pipeline is not running", reason, "Expected reason to explain why unhealthy")
}

func TestAPIOptions(t *testing.T) {
	a := newAPIv1()
	WithPort(8081)(a)
	WithHost("127.0.0.1")(a)
	WithGroup("api-test")(a)
	assert.NotNil(t, a)
	assert.Equal(t, uint(8081), a.Port)
	assert.Equal(t, "127.0.0.1", a.Host)
	assert.Equal(t, "api-test", a.basePath)
	assert.Equal(t, "api-test", a.BasePath())
}

func TestAPIHandleFunc(t *testing.T) {
	a := New(true)
	a.HandleFunc("GET /custom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("custom"))
	})
	req, err := http.NewRequest(http.MethodGet, "/custom", nil)
	require.NoError(t, err)
	rr := httptest.NewRecorder()
	a.Engine().ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "custom", rr.Body.String())
}

func TestAPIPanicRecovery(t *testing.T) {
	a := New(true)
	a.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
		panic("simulated panic")
	})
	req, err := http.NewRequest(http.MethodGet, "/panic", nil)
	require.NoError(t, err)
	rr := httptest.NewRecorder()
	a.Engine().ServeHTTP(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "", rr.Body.String())
}

func TestClientIP(t *testing.T) {
	reqXFF, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	reqXFF.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.1")
	assert.Equal(t, "192.168.1.1", clientIP(reqXFF))

	reqXRI, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	reqXRI.Header.Set("X-Real-IP", "172.16.0.1")
	assert.Equal(t, "172.16.0.1", clientIP(reqXRI))

	reqFallback, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	reqFallback.RemoteAddr = "1.2.3.4:5678"
	assert.Equal(t, "1.2.3.4", clientIP(reqFallback))
}

type mockHijackerFlusher struct {
	httptest.ResponseRecorder
	flushed  bool
	hijacked bool
}

func (m *mockHijackerFlusher) Flush() {
	m.flushed = true
}

func (m *mockHijackerFlusher) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	return nil, nil, nil
}

func TestResponseRecorder_FlushAndHijack(t *testing.T) {
	// 1. Standard ResponseWriter that implements Flusher but not Hijacker
	// httptest.ResponseRecorder has Flush() but not Hijack()
	recorderBase := httptest.NewRecorder()
	recFlusher := newResponseRecorder(recorderBase)

	_, okFlusher := recFlusher.(http.Flusher)
	assert.True(t, okFlusher)
	_, okHijacker := recFlusher.(http.Hijacker)
	assert.False(t, okHijacker)

	recFlusher.(http.Flusher).Flush() // No-op, shouldn't panic

	// 2. Mock recorder that implements both
	mockBase := &mockHijackerFlusher{}
	recBoth := newResponseRecorder(mockBase)

	_, okFlusher = recBoth.(http.Flusher)
	assert.True(t, okFlusher)
	_, okHijacker = recBoth.(http.Hijacker)
	assert.True(t, okHijacker)

	recBoth.(http.Flusher).Flush()
	assert.True(t, mockBase.flushed)

	conn, rw, err := recBoth.(http.Hijacker).Hijack()
	assert.NoError(t, err)
	assert.True(t, mockBase.hijacked)
	assert.Nil(t, conn)
	assert.Nil(t, rw)
}

// mockResponseWriterNoFlusher does NOT implement http.Flusher
type mockResponseWriterNoFlusher struct {
	http.ResponseWriter
}

func TestHandleSSE_NoFlusher(t *testing.T) {
	hub := NewEventHub(10)
	defer hub.Close()

	req := httptest.NewRequest(http.MethodGet, "/events?format=sse", nil)
	recorder := httptest.NewRecorder()
	rec := &mockResponseWriterNoFlusher{ResponseWriter: recorder}

	hub.HandleEvents(rec, req)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "streaming unsupported")
}

func TestHandleWebSocket_UpgradeFailure(t *testing.T) {
	hub := NewEventHub(10)
	defer hub.Close()

	// Set Upgrade and Connection headers to bypass IsWebSocketUpgrade check,
	// but omit Sec-WebSocket-Key/Version so the actual handshake fails.
	req := httptest.NewRequest(http.MethodGet, "/events?format=websocket", nil)
	req.Header.Set("Connection", "upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()

	hub.HandleEvents(rec, req)

	// Under gorilla/websocket, upgrade failure returns 400 Bad Request
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWriteJSON_MarshallingFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	// Functions cannot be marshalled to JSON
	unmarshallable := func() {}
	WriteJSON(recorder, http.StatusOK, unmarshallable)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}

func TestNewEventHub_DefaultBounds(t *testing.T) {
	// 0 buffer size should fall back to default
	hubZero := NewEventHub(0)
	defer hubZero.Close()
	assert.NotNil(t, hubZero)
	assert.Equal(t, defaultRingSize, hubZero.ringSize)
	assert.Len(t, hubZero.ring, defaultRingSize)

	// A non-zero size is honored verbatim.
	hubSized := NewEventHub(7)
	defer hubSized.Close()
	assert.Equal(t, 7, hubSized.ringSize)
	assert.Len(t, hubSized.ring, 7)
}

func TestResponseRecorder_MultipleWrites(t *testing.T) {
	recorderBase := httptest.NewRecorder()
	rec := newResponseRecorder(recorderBase)
	recState := rec.(recorder)

	// First write should set recState.wrote to true
	n, err := rec.Write([]byte("first"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, http.StatusOK, recState.Status())
	assert.True(t, recState.Wrote())

	// Second write
	n2, err := rec.Write([]byte("second"))
	assert.NoError(t, err)
	assert.Equal(t, 6, n2)
	assert.Equal(t, http.StatusOK, recState.Status())
}

// newMiddlewareEventsServer mounts the /events handler behind the production
// withMiddleware wrapper (and thus newResponseRecorder), so tests exercise the
// same Flusher/Hijacker forwarding path used in production rather than a bare
// mux.
func newMiddlewareEventsServer(hub *EventHub) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", hub.HandleEvents)
	return httptest.NewServer(withMiddleware(mux))
}

// TestIntegration_WebSocketThroughMiddleware verifies the WebSocket upgrade
// (which requires http.Hijacker) survives the middleware-wrapped recorder.
func TestIntegration_WebSocketThroughMiddleware(t *testing.T) {
	hub := NewEventHub(10)
	defer hub.Close()
	server := newMiddlewareEventsServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/events"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	hub.Broadcast(event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
		Payload:   map[string]string{"hash": "abc123"},
	})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var received event.Event
	require.NoError(t, json.Unmarshal(msg, &received))
	assert.Equal(t, "input.block", received.Type)
}

// TestIntegration_SSEThroughMiddleware verifies SSE (which requires
// http.Flusher) survives the middleware-wrapped recorder.
func TestIntegration_SSEThroughMiddleware(t *testing.T) {
	hub := NewEventHub(10)
	defer hub.Close()
	server := newMiddlewareEventsServer(hub)
	defer server.Close()

	// replay=false so the first data frame is the event we broadcast below.
	resp, err := http.Get(server.URL + "/events?replay=false")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	hub.Broadcast(event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
		Payload:   map[string]string{"hash": "xyz789"},
	})

	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				ch <- readResult{err: readErr}
				return
			}
			if strings.HasPrefix(line, "data: ") {
				ch <- readResult{line: line}
				return
			}
		}
	}()

	select {
	case res := <-ch:
		require.NoError(t, res.err)
		assert.Contains(t, res.line, "input.block")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE data frame")
	}
}
