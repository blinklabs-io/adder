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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// StatusTracker tests
// ---------------------------------------------------------------------------

func TestStatusTracker_InitialState(t *testing.T) {
	st := NewStatusTracker()
	assert.Equal(t, StatusStopped, st.Get())
}

func TestStatusTracker_SetGet(t *testing.T) {
	st := NewStatusTracker()

	st.Set(StatusStarting)
	assert.Equal(t, StatusStarting, st.Get())

	st.Set(StatusConnected)
	assert.Equal(t, StatusConnected, st.Get())

	st.Set(StatusError)
	assert.Equal(t, StatusError, st.Get())
}

func TestStatusTracker_OnChange(t *testing.T) {
	st := NewStatusTracker()

	var mu sync.Mutex
	var captured []Status

	st.OnChange(func(s Status) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, s)
	})

	st.Set(StatusStarting)
	st.Set(StatusConnected)
	// Setting to the same value should not trigger callback
	st.Set(StatusConnected)
	st.Set(StatusError)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []Status{StatusStarting, StatusConnected, StatusError}, captured)
}

func TestStatusTracker_String(t *testing.T) {
	statuses := []Status{
		StatusStopped,
		StatusStarting,
		StatusConnected,
		StatusReconnecting,
		StatusError,
	}

	for _, s := range statuses {
		assert.NotEmpty(t, s.String(), "Status %q should have a non-empty string", s)
	}
}

// ---------------------------------------------------------------------------
// EventParser tests
// ---------------------------------------------------------------------------

func TestEventParser_ValidJSON(t *testing.T) {
	r, w := io.Pipe()
	ep := NewEventParser(r, 1024*1024)
	ep.Start()
	t.Cleanup(func() {
		ep.Stop()
	})

	evt := map[string]any{
		"type":      "chainsync.block",
		"timestamp": "2026-01-15T10:30:00Z",
		"payload":   map[string]any{"slot": 12345},
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	_, err = fmt.Fprintf(w, "%s\n", data)
	require.NoError(t, err)

	select {
	case parsed, ok := <-ep.Events():
		require.True(t, ok, "events channel should be open")
		assert.Equal(t, "chainsync.block", parsed.Type)
		assert.False(t, parsed.Timestamp.IsZero())
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}

	require.NoError(t, w.Close())
}

func TestEventParser_MalformedLines(t *testing.T) {
	r, w := io.Pipe()
	ep := NewEventParser(r, 1024*1024)
	ep.Start()
	t.Cleanup(func() {
		ep.Stop()
	})

	// Write malformed line followed by a valid line
	_, err := fmt.Fprintln(w, "this is not json")
	require.NoError(t, err)

	evt := map[string]any{
		"type":      "chainsync.rollback",
		"timestamp": "2026-01-15T10:31:00Z",
		"payload":   map[string]any{},
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	_, err = fmt.Fprintf(w, "%s\n", data)
	require.NoError(t, err)

	// Should skip malformed and deliver valid event
	select {
	case parsed, ok := <-ep.Events():
		require.True(t, ok)
		assert.Equal(t, "chainsync.rollback", parsed.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event after malformed line")
	}

	require.NoError(t, w.Close())
}

func TestEventParser_EOF(t *testing.T) {
	r := strings.NewReader("")
	ep := NewEventParser(r, 1024*1024)
	ep.Start()

	// Channel should close on EOF
	select {
	case _, ok := <-ep.Events():
		assert.False(t, ok, "events channel should be closed on EOF")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for events channel to close")
	}
}

// ---------------------------------------------------------------------------
// HealthPoller tests
// ---------------------------------------------------------------------------

func TestHealthPoller_URLConstruction(t *testing.T) {
	tracker := NewStatusTracker()
	hp := NewHealthPoller("127.0.0.1", 8080, tracker)
	assert.Equal(t, "http://127.0.0.1:8080/healthcheck", hp.healthURL())
}

func TestHealthPoller_HealthyResponse(t *testing.T) {
	tracker := NewStatusTracker()
	tracker.Set(StatusStarting)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"failed":false}`))
	}))
	defer srv.Close()

	// Parse the test server address
	hp := newHealthPollerFromURL(t, srv.URL, tracker)
	hp.poll()

	assert.Equal(t, StatusConnected, tracker.Get())
}

func TestHealthPoller_UnhealthyResponse(t *testing.T) {
	tracker := NewStatusTracker()
	tracker.Set(StatusStarting)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"failed":true,"reason":"pipeline is not running"}`))
	}))
	defer srv.Close()

	hp := newHealthPollerFromURL(t, srv.URL, tracker)

	// First two polls should not yet set error
	hp.poll()
	assert.NotEqual(t, StatusError, tracker.Get())
	hp.poll()
	assert.NotEqual(t, StatusError, tracker.Get())

	// Third consecutive failure should set error
	hp.poll()
	assert.Equal(t, StatusError, tracker.Get())
}

func TestHealthPoller_RecoveryAfterError(t *testing.T) {
	tracker := NewStatusTracker()
	tracker.Set(StatusStarting)

	var mu sync.Mutex
	healthy := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		isHealthy := healthy
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if isHealthy {
			_, _ = w.Write([]byte(`{"failed":false}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"failed":true,"reason":"pipeline is not running"}`))
		}
	}))
	defer srv.Close()

	hp := newHealthPollerFromURL(t, srv.URL, tracker)

	// Drive to error state
	hp.poll()
	hp.poll()
	hp.poll()
	assert.Equal(t, StatusError, tracker.Get())

	// Switch to healthy
	mu.Lock()
	healthy = true
	mu.Unlock()

	hp.poll()
	assert.Equal(t, StatusConnected, tracker.Get())
}

// newHealthPollerFromURL creates a HealthPoller targeting a test server.
func newHealthPollerFromURL(
	t *testing.T,
	serverURL string,
	tracker *StatusTracker,
) *HealthPoller {
	t.Helper()

	u, err := url.Parse(serverURL)
	require.NoError(t, err, "failed to parse test server URL")

	host := u.Hostname()
	portStr := u.Port()
	portVal, err := strconv.ParseUint(portStr, 10, 32)
	require.NoError(t, err, "failed to parse port from test server URL")

	hp := &HealthPoller{
		address: host,
		port:    uint(portVal),
		tracker: tracker,
		client: &http.Client{
			Timeout: healthHTTPTimeout,
		},
		stopCh: make(chan struct{}),
	}
	return hp
}

// ---------------------------------------------------------------------------
// Backoff tests
// ---------------------------------------------------------------------------

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		restartCount int
		expected     time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, 60 * time.Second},  // capped
		{7, 60 * time.Second},  // still capped
		{10, 60 * time.Second}, // still capped
	}

	for _, tt := range tests {
		t.Run(
			fmt.Sprintf("count_%d", tt.restartCount),
			func(t *testing.T) {
				got := backoffDelay(tt.restartCount)
				assert.Equal(t, tt.expected, got)
			},
		)
	}
}
