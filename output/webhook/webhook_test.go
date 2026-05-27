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

package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
