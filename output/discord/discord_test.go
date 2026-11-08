// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package discord

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type discordRoundTripFunc func(*http.Request) (*http.Response, error)

func (f discordRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestDiscordPublicV10Endpoint(t *testing.T) {
	client := &http.Client{Transport: discordRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https://discord.com/api/v10/gateway/bot", req.URL.String())
		require.Equal(t, "Bot secret", req.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"url":"wss://gateway.discord.gg"}`))}, nil
	})}
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithHTTPClient(client))
	require.NoError(t, err)
	data, err := d.request(context.Background(), http.MethodGet, "/gateway/bot", nil, true)
	require.NoError(t, err)
	require.JSONEq(t, `{"url":"wss://gateway.discord.gg"}`, string(data))
}

func TestDiscordConfiguration(t *testing.T) {
	for _, tt := range []struct{ token, app, guild, channel, part string }{
		{"", "1", "2", "3", "bot-token"},
		{"secret", "bad", "2", "3", "application-id"},
		{"secret", "1", "0", "3", "guild-id"},
		{"secret", "1", "2", "bad", "channel-id"},
	} {
		_, err := New(WithCredentials(tt.token, tt.app, tt.guild, tt.channel))
		require.ErrorContains(t, err, tt.part)
		require.NotContains(t, err.Error(), "secret")
	}
	require.Error(t, validateGatewayURL("https://example.com/gateway"))
	require.Error(t, validateGatewayURL("wss://user:secret@example.com/gateway"))
	require.Equal(t, "wss://example.com/gateway?encoding=json&v=10", gatewayURL("wss://example.com/gateway"))
}

func TestDiscordRateLimitAndSelection(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bot secret", r.Header.Get("Authorization"))
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"retry_after":0.01}`)
			return
		}
		if r.URL.Path == "/channels/3/messages" {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithAPIBaseURL(server.URL))
	require.NoError(t, err)
	d.deliverEvent(context.Background(), event.Event{Type: event.TypeBlock, Payload: event.BlockEvent{BlockHash: "hash"}, Context: event.BlockContext{BlockNumber: 42, SlotNumber: 77}})
	mu.Lock()
	count := calls
	mu.Unlock()
	require.Equal(t, 2, count)
	require.Equal(t, []any{}, body["allowed_mentions"].(map[string]any)["parse"])
	require.Len(t, body["embeds"], 1)
	d.deliverEvent(context.Background(), event.Event{Type: "not-selected"})
	mu.Lock()
	count = calls
	mu.Unlock()
	require.Equal(t, 2, count)
	require.Equal(t, []string{"Block 42 at slot 77"}, d.recent)
}

func TestDiscordGatewayCommands(t *testing.T) {
	var mu sync.Mutex
	commands := []string{}
	callbacks := make(chan map[string]any, 4)
	messageSent := make(chan struct{})
	identified := make(chan map[string]any, 1)
	gatewayDone := make(chan struct{})
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gateway":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			require.Equal(t, "10", r.URL.Query().Get("v"))
			require.Equal(t, "json", r.URL.Query().Get("encoding"))
			require.NoError(t, conn.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 200}}))
			var identify map[string]any
			require.NoError(t, conn.ReadJSON(&identify))
			identified <- identify
			wsURL := "wss" + strings.TrimPrefix(server.URL, "https") + "/gateway"
			require.NoError(t, conn.WriteJSON(map[string]any{"op": 0, "t": "READY", "s": 1, "d": map[string]any{"session_id": "session", "resume_gateway_url": wsURL}}))
			select {
			case <-messageSent:
			case <-time.After(5 * time.Second):
				return
			}
			for i, item := range []struct{ guild, channel, name string }{{"2", "3", "adder-status"}, {"2", "3", "adder-recent"}, {"2", "4", "adder-status"}, {"9", "3", "adder-status"}} {
				payload := map[string]any{"id": fmt.Sprintf("%d", i+10), "token": fmt.Sprintf("token%d", i), "application_id": "1", "guild_id": item.guild, "channel_id": item.channel, "type": 2, "data": map[string]any{"name": item.name}}
				require.NoError(t, conn.WriteJSON(map[string]any{"op": 0, "t": "INTERACTION_CREATE", "s": i + 2, "d": payload}))
			}
			for {
				var incoming map[string]any
				if conn.ReadJSON(&incoming) != nil {
					close(gatewayDone)
					return
				}
				if incoming["op"] == float64(1) {
					_ = conn.WriteJSON(map[string]any{"op": 11, "d": nil})
				}
			}
		case r.URL.Path == "/applications/1/guilds/2/commands":
			require.Equal(t, "Bot secret", r.Header.Get("Authorization"))
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			mu.Lock()
			commands = append(commands, body["name"].(string))
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/gateway/bot":
			require.Equal(t, "Bot secret", r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{"url": "wss" + strings.TrimPrefix(server.URL, "https") + "/gateway"})
		case r.URL.Path == "/channels/3/messages":
			require.Equal(t, "Bot secret", r.Header.Get("Authorization"))
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Len(t, body["embeds"], 1)
			close(messageSent)
			w.WriteHeader(http.StatusCreated)
		case strings.HasPrefix(r.URL.Path, "/interactions/"):
			require.Empty(t, r.Header.Get("Authorization"))
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if strings.HasPrefix(r.URL.Path, "/interactions/10/") {
				time.Sleep(350 * time.Millisecond)
			}
			callbacks <- body
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected REST path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithAPIBaseURL(server.URL), WithHTTPClient(server.Client()), WithGatewayDialer(&websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, d.StartContext(ctx))
	select {
	case frame := <-identified:
		require.Equal(t, float64(2), frame["op"])
		data := frame["d"].(map[string]any)
		require.Equal(t, float64(0), data["intents"])
	case <-time.After(5 * time.Second):
		t.Fatal("Gateway Identify not received")
	}
	d.InputChan() <- event.Event{Type: event.TypeBlock, Payload: event.BlockEvent{BlockHash: "hash"}, Context: event.BlockContext{BlockNumber: 1, SlotNumber: 2}}
	var replies []string
	for i := 0; i < 3; i++ {
		select {
		case body := <-callbacks:
			require.Equal(t, float64(4), body["type"])
			data := body["data"].(map[string]any)
			require.Equal(t, float64(64), data["flags"])
			replies = append(replies, data["content"].(string))
		case <-time.After(5 * time.Second):
			t.Fatal("missing interaction callback")
		}
	}
	require.NotEqual(t, "Adder Discord output is running.", replies[0], "slow first callback must not block later interactions")
	require.Contains(t, replies, "Adder Discord output is running.")
	require.Contains(t, replies, "Recent delivered events:\nBlock 1 at slot 2")
	require.Contains(t, replies, "This command is available only in the configured channel.")
	select {
	case <-callbacks:
		t.Fatal("unauthorized guild got a callback")
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	require.Equal(t, []string{"adder-status", "adder-recent"}, commands)
	mu.Unlock()
	cancel()
	require.NoError(t, d.Stop())
	select {
	case <-gatewayDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Gateway did not stop")
	}
}

func TestDiscordRetryDelay(t *testing.T) {
	for _, tt := range []struct {
		header, body string
		want         time.Duration
		ok           bool
	}{{"0.25", "", 250 * time.Millisecond, true}, {"", `{"retry_after":0.5}`, 500 * time.Millisecond, true}, {"", `{}`, 0, false}, {"bad", `bad`, 0, false}, {"-1", "", 0, false}} {
		actual, ok := retryDelay(tt.header, []byte(tt.body))
		require.Equal(t, tt.ok, ok)
		require.Equal(t, tt.want, actual)
	}
}

func TestDiscordGatewayResumesSession(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	resumed := make(chan map[string]any, 1)
	var mu sync.Mutex
	connections := 0
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mu.Lock()
		connections++
		n := connections
		mu.Unlock()
		_ = conn.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 1000}})
		var frame map[string]any
		if conn.ReadJSON(&frame) != nil {
			return
		}
		if n == 1 {
			if frame["op"] != float64(2) {
				t.Errorf("first Gateway frame: %v", frame)
				return
			}
			_ = conn.WriteJSON(map[string]any{"op": 0, "t": "READY", "s": 42, "d": map[string]any{"session_id": "session-42", "resume_gateway_url": "wss" + strings.TrimPrefix(server.URL, "https")}})
			_ = conn.WriteJSON(map[string]any{"op": 9, "d": true})
			return
		}
		resumed <- frame
		for {
			if conn.ReadJSON(&frame) != nil {
				return
			}
			if frame["op"] == float64(1) {
				_ = conn.WriteJSON(map[string]any{"op": 11})
			}
		}
	}))
	defer server.Close()
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithGatewayDialer(&websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { d.gatewayLoop(ctx, "wss"+strings.TrimPrefix(server.URL, "https")); close(finished) }()
	select {
	case frame := <-resumed:
		require.Equal(t, float64(6), frame["op"])
		data := frame["d"].(map[string]any)
		require.Equal(t, "session-42", data["session_id"])
		require.Equal(t, float64(42), data["seq"])
	case <-time.After(5 * time.Second):
		t.Fatal("Gateway did not resume")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Gateway did not stop after cancellation")
	}
}

func TestDiscordInteractionBypassesBotGlobalCooldown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/channels/") {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"global": true, "retry_after": 30}`)
			return
		}
		require.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithAPIBaseURL(server.URL))
	require.NoError(t, err)
	_, err = d.request(context.Background(), http.MethodPost, "/channels/3/messages", []byte(`{}`), true)
	require.ErrorContains(t, err, "cooling down")
	start := time.Now()
	_, err = d.request(context.Background(), http.MethodPost, "/interactions/10/token/callback", []byte(`{}`), false)
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second)
}

func TestDiscordInvalidSessionRequiresIdentify(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	frames := make(chan map[string]any, 1)
	var mu sync.Mutex
	connections := 0
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mu.Lock()
		connections++
		n := connections
		mu.Unlock()
		_ = conn.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 1000}})
		var frame map[string]any
		if conn.ReadJSON(&frame) != nil {
			return
		}
		if n == 1 {
			_ = conn.WriteJSON(map[string]any{"op": 0, "t": "READY", "s": 42, "d": map[string]any{"session_id": "session-42", "resume_gateway_url": "wss" + strings.TrimPrefix(server.URL, "https")}})
			_ = conn.WriteJSON(map[string]any{"op": 9, "d": false})
			return
		}
		frames <- frame
		_ = conn.WriteJSON(map[string]any{"op": 9, "d": false})
	}))
	defer server.Close()
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithGatewayDialer(&websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var session gatewaySession
	require.NoError(t, d.runGateway(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &session))
	require.Empty(t, session.id)
	require.NoError(t, d.runGateway(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &session))
	select {
	case frame := <-frames:
		require.Equal(t, float64(2), frame["op"])
	case <-ctx.Done():
		t.Fatal("Identify not received")
	}
}

func TestDiscordGatewayHeartbeatUsesLatestSequence(t *testing.T) {
	upgrader := websocket.Upgrader{}
	heartbeats := make(chan map[string]any, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_ = conn.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 150}})
		var frame map[string]any
		if conn.ReadJSON(&frame) != nil {
			return
		}
		if frame["op"] != float64(2) {
			t.Errorf("expected Identify, got %v", frame)
			return
		}
		_ = conn.WriteJSON(map[string]any{"op": 0, "t": "READY", "s": 99, "d": map[string]any{"session_id": "session", "resume_gateway_url": "wss://example.com"}})
		for i := 0; i < 2; i++ {
			if conn.ReadJSON(&frame) != nil {
				return
			}
			heartbeats <- frame
			_ = conn.WriteJSON(map[string]any{"op": 11, "d": nil})
		}
	}))
	defer server.Close()
	d, err := New(WithCredentials("secret", "1", "2", "3"), WithGatewayDialer(&websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var session gatewaySession
	_ = d.runGateway(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &session)
	for i := 0; i < 2; i++ {
		select {
		case frame := <-heartbeats:
			require.Equal(t, float64(1), frame["op"])
			require.Equal(t, float64(99), frame["d"])
		case <-ctx.Done():
			t.Fatal("missing heartbeat")
		}
	}
}
