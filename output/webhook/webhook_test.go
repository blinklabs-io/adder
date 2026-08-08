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

package webhook

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mainnetNetworkMagic = 764824073

// fastRetry disables backoff sleeping so error paths return promptly.
func fastRetry() WebhookOptionFunc {
	return WithRetryConfig(0, time.Millisecond, time.Millisecond)
}

func blockEvent() event.Event {
	return event.New(
		"input.block",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		event.BlockContext{
			Era:          "Conway",
			BlockNumber:  100,
			SlotNumber:   200,
			NetworkMagic: mainnetNetworkMagic,
		},
		event.BlockEvent{
			BlockHash:  "deadbeef",
			IssuerVkey: "vkey1",
		},
	)
}

// --- Success-path regression guards --------------------------------------

func TestFormatWebhookAdderSuccess(t *testing.T) {
	e := blockEvent()
	data, err := formatWebhook(&e, "adder")
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var parsed event.Event
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "input.block", parsed.Type)
}

func TestFormatWebhookDiscordSuccess(t *testing.T) {
	cases := []struct {
		name string
		evt  event.Event
	}{
		{"block", blockEvent()},
		{
			"rollback",
			event.New("input.rollback", time.Now(), nil,
				event.RollbackEvent{BlockHash: "abc", SlotNumber: 5}),
		},
		{
			"transaction",
			event.New("input.transaction", time.Now(),
				event.TransactionContext{TransactionHash: "tx", BlockNumber: 1},
				event.TransactionEvent{Fee: 10}),
		},
		{
			"governance",
			event.New("input.governance", time.Now(),
				event.GovernanceContext{TransactionHash: "gtx", BlockNumber: 1},
				event.GovernanceEvent{}),
		},
		{
			"default",
			event.New("input.other", time.Now(), nil, "raw-payload"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := formatWebhook(&tc.evt, "discord")
			require.NoError(t, err)
			var dwe DiscordWebhookEvent
			require.NoError(t, json.Unmarshal(data, &dwe))
		})
	}
}

func TestSendWebhookSuccess(t *testing.T) {
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	defer srv.Close()

	w := New(WithUrl(srv.URL, false))
	e := blockEvent()
	require.NoError(t, w.SendWebhook(&e))
}

// --- Error-path tests ----------------------------------------------------

func TestFormatWebhookDiscordBadPayloadType(t *testing.T) {
	// input.block with a payload that is not a BlockEvent
	e := event.New(
		"input.block",
		time.Now(),
		event.BlockContext{},
		"not-a-block-event",
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected payload type")
}

func TestFormatWebhookDiscordBadContextType(t *testing.T) {
	// input.block with a valid payload but wrong context type
	e := event.New(
		"input.block",
		time.Now(),
		"not-a-block-context",
		event.BlockEvent{BlockHash: "h"},
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected context type")
}

func TestFormatWebhookMarshalError(t *testing.T) {
	// A float NaN cannot be marshaled to JSON; embed it in the default
	// (non-discord) path via an unsupported payload value.
	e := event.New("input.other", time.Now(), nil, math.NaN())
	data, err := formatWebhook(&e, "adder")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "failed to marshal")
}

func TestSendWebhookFormatError(t *testing.T) {
	// SendWebhook must propagate formatWebhook errors instead of POSTing.
	e := event.New(
		"input.block",
		time.Now(),
		event.BlockContext{},
		"not-a-block-event",
	)
	w := New(WithFormat("discord"), WithUrl("http://127.0.0.1:0", false))
	err := w.SendWebhook(&e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected payload type")
}

func TestSendWebhookConnectionRefused(t *testing.T) {
	// Point at a server we immediately close so the POST fails.
	srv := httptest.NewServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	url := srv.URL
	srv.Close()

	w := New(WithUrl(url, false))
	e := blockEvent()
	require.Error(t, w.SendWebhook(&e))
}

func TestSendWebhookBadURL(t *testing.T) {
	// A control character in the URL fails request construction.
	w := New(WithUrl("http://example.com/\x7f", false))
	e := blockEvent()
	require.Error(t, w.SendWebhook(&e))
}

// drainErr starts a reader on the plugin error channel and returns a
// function that blocks until one error arrives (or times out). The plugin
// sends errors non-blockingly on an unbuffered channel, so the reader is
// started (and given a chance to park on the receive) before the caller
// feeds the input that triggers the error.
func drainErr(t *testing.T, w *WebhookOutput) func() error {
	t.Helper()
	var (
		mu    sync.Mutex
		got   error
		done  = make(chan struct{})
		ready = make(chan struct{})
	)
	go func() {
		errChan := w.ErrorChan()
		close(ready)
		select {
		case e := <-errChan:
			mu.Lock()
			got = e
			mu.Unlock()
		case <-time.After(3 * time.Second):
		}
		close(done)
	}()
	// Wait for the reader goroutine to start and yield so it has parked on
	// the receive before the triggering event is delivered.
	<-ready
	runtime.Gosched()
	return func() error {
		<-done
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func TestStartNilPayloadSurfacesError(t *testing.T) {
	w := New(fastRetry())
	require.NoError(t, w.Start())
	wait := drainErr(t, w)
	w.InputChan() <- event.New("input.block", time.Now(), event.BlockContext{}, nil)
	err := wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil payload")
	require.NoError(t, w.Stop())
}

func TestStartNilContextSurfacesError(t *testing.T) {
	w := New(fastRetry())
	require.NoError(t, w.Start())
	wait := drainErr(t, w)
	w.InputChan() <- event.New(
		"input.block", time.Now(), nil, event.BlockEvent{BlockHash: "h"},
	)
	err := wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil context")
	require.NoError(t, w.Stop())
}

func TestStartBadPayloadTypeSurfacesError(t *testing.T) {
	w := New(fastRetry())
	require.NoError(t, w.Start())
	wait := drainErr(t, w)
	w.InputChan() <- event.New(
		"input.transaction", time.Now(), event.TransactionContext{}, "bogus",
	)
	err := wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected payload type")
	require.NoError(t, w.Stop())
}

func TestStartDeliveryFailureSurfacesError(t *testing.T) {
	// Valid event but no reachable server -> retries exhaust -> error.
	srv := httptest.NewServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	url := srv.URL
	srv.Close()

	w := New(WithUrl(url, false), fastRetry())
	require.NoError(t, w.Start())
	wait := drainErr(t, w)
	e := blockEvent()
	w.InputChan() <- e
	err := wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed after")
	require.NoError(t, w.Stop())
}

func TestStartUnknownEventTypeSurfacesError(t *testing.T) {
	// Unknown types are now reported as errors.
	w := New(fastRetry())
	require.NoError(t, w.Start())
	wait := drainErr(t, w)
	w.InputChan() <- event.New("input.bogus", time.Now(), nil, "x")
	err := wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown event type")
	require.NoError(t, w.Stop())
}

func TestWebhookOutput_Start(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := New(
		WithUrl(server.URL, false),
		WithFormat("adder"),
	)

	err := w.Start()
	require.NoError(t, err)
	defer w.Stop()

	evt := event.Event{
		Type:      "input.block",
		Timestamp: time.Now(),
		Context: event.BlockContext{
			BlockNumber: 100,
		},
		Payload: event.BlockEvent{
			BlockHash: "test-hash",
		},
	}

	w.InputChan() <- evt

	select {
	case body := <-received:
		var receivedEvt event.Event
		err := json.Unmarshal(body, &receivedEvt)
		require.NoError(t, err)
		assert.Equal(t, "input.block", receivedEvt.Type)
		// Payloads might be unmarshaled as map[string]any
		payload, ok := receivedEvt.Payload.(map[string]any)
		require.True(t, ok, "expected payload to be map[string]any")
		assert.Equal(t, "test-hash", payload["blockHash"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

func TestWebhookOutput_Retry(t *testing.T) {
	var callCount atomic.Int32
	attempts := make(chan struct{}, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		attempts <- struct{}{}
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := New(
		WithUrl(server.URL, false),
		WithRetryConfig(3, 10*time.Millisecond, 1*time.Second),
	)

	err := w.Start()
	require.NoError(t, err)
	defer w.Stop()

	evt := event.Event{
		Type:    "input.rollback",
		Payload: event.RollbackEvent{},
	}

	w.InputChan() <- evt

	count := 0
	timeout := time.After(2 * time.Second)
LOOP:
	for {
		select {
		case <-attempts:
			count++
			if count >= 3 {
				break LOOP
			}
		case <-timeout:
			t.Fatalf("timed out waiting for retries, got %d attempts", count)
		}
	}
	assert.Equal(t, int32(3), callCount.Load())
}

func TestWebhookOutput_BasicAuth(t *testing.T) {
	authorized := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if ok && username == "user" && password == "pass" {
			authorized <- true
		} else {
			authorized <- false
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := New(
		WithUrl(server.URL, false),
		WithBasicAuth("user", "pass"),
	)

	err := w.Start()
	require.NoError(t, err)
	defer w.Stop()

	w.InputChan() <- event.Event{
		Type:    "input.rollback",
		Payload: event.RollbackEvent{},
	}

	select {
	case ok := <-authorized:
		assert.True(t, ok)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for auth")
	}
}

func TestWebhookOutput_ShutdownDuringInFlightRequest(t *testing.T) {
	// 1. Slow server (stalls for 100ms)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := New(
		WithUrl(srv.URL, false),
		WithRetryConfig(0, time.Millisecond, time.Millisecond),
	)

	require.NoError(t, w.Start())

	// 2. Send an event
	evt := event.Event{
		Type:    "input.rollback",
		Payload: event.RollbackEvent{},
	}
	w.InputChan() <- evt

	// 3. Wait briefly for worker to pick up the event and enter SendWebhook
	time.Sleep(10 * time.Millisecond)

	// 4. Stop concurrently while request is in flight
	require.NoError(t, w.Stop())

	// Sleep slightly to let mock finish and make sure no races or panics happen
	time.Sleep(150 * time.Millisecond)
}

// TestWebhookDeliverySuccess verifies successful HTTP POST delivery to a webhook endpoint.
func TestWebhookDeliverySuccess(t *testing.T) {
	type receivedRequest struct {
		body   []byte
		method string
	}
	received := make(chan receivedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- receivedRequest{body: body, method: r.Method}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	w := New(
		WithUrl(server.URL, false),
		WithFormat("adder"),
	)
	require.NoError(t, w.Start())
	t.Cleanup(func() { _ = w.Stop() })

	be := blockEvent()
	w.InputChan() <- be

	select {
	case req := <-received:
		assert.Equal(t, http.MethodPost, req.method)
		var receivedEvt event.Event
		err := json.Unmarshal(req.body, &receivedEvt)
		require.NoError(t, err)
		assert.Equal(t, "input.block", receivedEvt.Type)
		// Payloads might be unmarshaled as map[string]any
		payload, ok := receivedEvt.Payload.(map[string]any)
		require.True(t, ok, "expected payload to be map[string]any")
		assert.Equal(t, "deadbeef", payload["blockHash"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

// TestWebhookDeliveryFailure verifies that HTTP 500 errors are surfaced on the error channel rather than panicking.
func TestWebhookDeliveryFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	w := New(
		WithUrl(server.URL, false),
		fastRetry(),
	)
	require.NoError(t, w.Start())
	t.Cleanup(func() { _ = w.Stop() })

	wait := drainErr(t, w)
	be := blockEvent()
	w.InputChan() <- be

	err := wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server returned status: 500")
}

// TestWebhookEventSerialization verifies that Block, Transaction, and Rollback events serialize to their expected JSON shapes.
func TestWebhookEventSerialization(t *testing.T) {
	received := make(chan []byte, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	w := New(
		WithUrl(server.URL, false),
		WithFormat("adder"),
	)
	require.NoError(t, w.Start())
	t.Cleanup(func() { _ = w.Stop() })

	// 1. BlockEvent
	w.InputChan() <- blockEvent()

	// 2. TransactionEvent
	w.InputChan() <- event.New(
		"input.transaction",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		event.TransactionContext{
			TransactionHash: "txdeadbeef",
			BlockNumber:     100,
			SlotNumber:      200,
			NetworkMagic:    mainnetNetworkMagic,
		},
		event.TransactionEvent{
			Fee: 180000,
		},
	)

	// 3. RollbackEvent
	w.InputChan() <- event.New(
		"input.rollback",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		nil,
		event.RollbackEvent{
			BlockHash:  "rollbackdeadbeef",
			SlotNumber: 200,
		},
	)

	for i := 0; i < 3; i++ {
		select {
		case body := <-received:
			var parsed event.Event
			err := json.Unmarshal(body, &parsed)
			require.NoError(t, err)

			switch parsed.Type {
			case "input.block":
				payload, ok := parsed.Payload.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "deadbeef", payload["blockHash"])
			case "input.transaction":
				payload, ok := parsed.Payload.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, float64(180000), payload["fee"])
			case "input.rollback":
				payload, ok := parsed.Payload.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "rollbackdeadbeef", payload["blockHash"])
			default:
				t.Fatalf("unexpected event type: %s", parsed.Type)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i+1)
		}
	}
}

// TestWebhookOutput_DoubleStart verifies that starting an already started plugin handles the transition and teardown cleanly.
func TestWebhookOutput_DoubleStart(t *testing.T) {
	w := New(fastRetry())
	require.NoError(t, w.Start())
	// Starting again should shut down the first worker and start cleanly
	require.NoError(t, w.Start())
	require.NoError(t, w.Stop())
}

// TestWebhookOutput_ReportErrorWithoutReader verifies that reportError logs the error if no consumer is reading from ErrorChan.
func TestWebhookOutput_ReportErrorWithoutReader(t *testing.T) {
	received := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(received)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	w := New(
		WithUrl(server.URL, false),
		fastRetry(),
	)
	require.NoError(t, w.Start())
	t.Cleanup(func() { _ = w.Stop() })

	// 1. Send malformed event to trigger error reporting but DO NOT read from ErrorChan()
	// This exercises the reportError default select case where error is logged.
	w.InputChan() <- event.New("input.block", time.Now(), nil, nil)

	// 2. Concurrently send a valid event through InputChan
	w.InputChan() <- event.Event{
		Type:    "input.rollback",
		Payload: event.RollbackEvent{},
	}

	// 3. Wait for the test server to receive the valid event, proving that the processing
	// loop was not blocked by the unread ErrorChan in the first event.
	select {
	case <-received:
		// Success!
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subsequent valid event delivery; processing loop was blocked")
	}

	require.NoError(t, w.Stop())
}

// TestFormatWebhookDiscordBadTransactionPayload verifies that formatting a transaction event with a malformed payload fails in Discord format mode.
func TestFormatWebhookDiscordBadTransactionPayload(t *testing.T) {
	e := event.New(
		"input.transaction",
		time.Now(),
		event.TransactionContext{},
		"not-a-transaction-event",
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected payload type")
}

// TestFormatWebhookDiscordBadTransactionContext verifies that formatting a transaction event with a malformed context fails in Discord format mode.
func TestFormatWebhookDiscordBadTransactionContext(t *testing.T) {
	e := event.New(
		"input.transaction",
		time.Now(),
		"not-a-transaction-context",
		event.TransactionEvent{},
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected context type")
}

// TestFormatWebhookDiscordBadGovernancePayload verifies that formatting a governance event with a malformed payload fails in Discord format mode.
func TestFormatWebhookDiscordBadGovernancePayload(t *testing.T) {
	e := event.New(
		"input.governance",
		time.Now(),
		event.GovernanceContext{},
		"not-a-governance-event",
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected payload type")
}

// TestFormatWebhookDiscordBadGovernanceContext verifies that formatting a governance event with a malformed context fails in Discord format mode.
func TestFormatWebhookDiscordBadGovernanceContext(t *testing.T) {
	e := event.New(
		"input.governance",
		time.Now(),
		"not-a-governance-context",
		event.GovernanceEvent{},
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected context type")
}

// TestFormatWebhookDiscordBadRollbackPayload verifies that formatting a rollback event with a malformed payload fails in Discord format mode.
func TestFormatWebhookDiscordBadRollbackPayload(t *testing.T) {
	e := event.New(
		"input.rollback",
		time.Now(),
		nil,
		"not-a-rollback-event",
	)
	data, err := formatWebhook(&e, "discord")
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unexpected payload type")
}

// TestNewFromCmdlineOptions verifies that the CLI registration factory creates a valid WebhookOutput instance.
func TestNewFromCmdlineOptions(t *testing.T) {
	p := NewFromCmdlineOptions()
	assert.NotNil(t, p)
	assert.IsType(t, &WebhookOutput{}, p)
}

// TestWebhookOutput_OutputChan verifies that OutputChan returns nil as webhook is a sink-only plugin.
func TestWebhookOutput_OutputChan(t *testing.T) {
	w := New()
	assert.Nil(t, w.OutputChan())
}


