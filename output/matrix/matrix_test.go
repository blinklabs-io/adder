// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestMatrixRoomSendContractAndFiltering(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer authorization")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON content type")
		}
		var body struct {
			MsgType string `json:"msgtype"`
			Body    string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.MsgType != "m.text" || !strings.Contains(body.Body, "Adder rollback") {
			t.Errorf("message = %+v", body)
		}
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"event_id":"$sent:example.org"}`))
	}))
	defer server.Close()
	m := New(WithHomeserver(server.URL), WithAccessToken("test-token"), WithRooms("!one:example.org", "!two:example.org"))
	require.NoError(t, m.Start())
	m.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"hash": "ignored"})
	m.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(paths) == 2 }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Stop())
	mu.Lock()
	defer mu.Unlock()
	for _, path := range paths {
		require.Contains(t, path, "/_matrix/client/v3/rooms/%21")
		require.Contains(t, path, "/send/m.room.message/adder-")
	}
	require.NotEqual(t, paths[0], paths[1], "each room needs its own transaction ID")
}

func TestMatrixRetryPreservesTransactionID(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		n := len(paths)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"event_id":"$sent:example.org"}`))
	}))
	defer server.Close()
	m := New(WithHomeserver(server.URL), WithAccessToken("test-token"), WithRooms("!one:example.org"))
	require.NoError(t, m.Start())
	m.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(paths) == 2 }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Stop())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, paths[0], paths[1])
}

func TestMatrixRateLimitWithoutDelayHint(t *testing.T) {
	for _, firstBody := range []string{"{}", `{"retry_after_ms":0}`} {
		t.Run(firstBody, func(t *testing.T) {
			attempts := make(chan string, 2)
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts <- r.URL.EscapedPath()
				if count.Add(1) == 1 {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(firstBody))
					return
				}
				_, _ = w.Write([]byte(`{"event_id":"$sent:example.org"}`))
			}))
			defer server.Close()
			m := New(WithHomeserver(server.URL), WithAccessToken("test-token"), WithRooms("!one:example.org"))
			require.NoError(t, m.Start())
			m.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
			var paths []string
			for len(paths) < 2 {
				select {
				case path := <-attempts:
					paths = append(paths, path)
				case <-time.After(3 * time.Second):
					t.Fatal("429 without a hint was not retried")
				}
			}
			require.Equal(t, paths[0], paths[1])
			require.NoError(t, m.Stop())
		})
	}
}

func TestMatrixHTTPDateRetryAfter(t *testing.T) {
	delay, err := parseRetryAfter(time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat))
	require.NoError(t, err)
	require.Greater(t, delay, 28*time.Second)
	require.LessOrEqual(t, delay, 30*time.Second)
}

func TestMatrixRequestTimeoutRetry(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		count := len(paths)
		mu.Unlock()
		if count == 1 {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		_, _ = w.Write([]byte(`{"event_id":"$sent:example.org"}`))
	}))
	defer server.Close()
	m := New(WithHomeserver(server.URL), WithAccessToken("test-token"), WithRooms("!one:example.org"))
	require.NoError(t, m.Start())
	m.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(paths) == 2 }, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Stop())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, paths[0], paths[1])
}

func TestMatrixEventTypesAreCopied(t *testing.T) {
	types := map[string]bool{event.TypeRollback: true}
	m := New(WithEventTypes(types))
	types[event.TypeRollback] = false
	require.True(t, m.selected[event.TypeRollback])
}

func TestMatrixRejectsInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct {
		values map[string]any
		match  string
	}{
		{map[string]any{"homeserver-url": "http://remote.example", "access-token": "secret", "room-ids": "!room:example.org"}, "HTTPS"},
		{map[string]any{"homeserver-url": "https://example.org:0", "access-token": "secret", "room-ids": "!room:example.org"}, "homeserver-url"},
		{map[string]any{"homeserver-url": "https://example.org", "room-ids": "!room:example.org"}, "access-token"},
		{map[string]any{"homeserver-url": "https://example.org", "access-token": "secret"}, "room-ids"},
		{map[string]any{"homeserver-url": "https://example.org", "access-token": "secret", "room-ids": "!room"}, "room-ids"},
		{map[string]any{"homeserver-url": "https://example.org", "access-token": "secret", "room-ids": "!room:example.org", "event-types": "bogus"}, "event type"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "matrix", tt.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tt.match)
	}
}
