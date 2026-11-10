// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0
package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDocumentedJSONRPCSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/rpc", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var req struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			ID      string `json:"id"`
			Params  struct {
				Account   string   `json:"account"`
				Recipient []string `json:"recipient"`
				Message   string   `json:"message"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "2.0", req.JSONRPC)
		require.Equal(t, "send", req.Method)
		require.Equal(t, "+15551234567", req.Params.Account)
		require.Equal(t, []string{"+15557654321"}, req.Params.Recipient)
		var evt event.Event
		require.NoError(t, json.Unmarshal([]byte(req.Params.Message), &evt))
		require.Equal(t, event.TypeRollback, evt.Type)
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"timestamp":999},"id":%q}`, req.ID)
	}))
	defer server.Close()
	s := New(WithEndpoint(server.URL+"/api/v1/rpc"), WithAccount("+15551234567"), WithRecipients("+15557654321"))
	require.NoError(t, s.deliver(context.Background(), event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})))
}

func TestJSONRPCErrorsAndCorrelation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"rpc-error", `{"jsonrpc":"2.0","error":{"code":-32602,"message":"bad recipient"},"id":%q}`, 200},
		{"missing-result", `{"jsonrpc":"2.0","id":%q}`, 200},
		{"wrong-id", `{"jsonrpc":"2.0","result":{"timestamp":999},"id":"wrong"}`, 200},
		{"malformed", `{`, 200},
		{"server-error", `failure`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID string `json:"id"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(fmt.Sprintf(tc.body, req.ID)))
			}))
			defer server.Close()
			s := New(WithEndpoint(server.URL+"/api/v1/rpc"), WithRecipients("+15557654321"))
			require.Error(t, s.deliver(context.Background(), event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{})))
		})
	}
}

func TestValidateEndpoint(t *testing.T) {
	for _, raw := range []string{"http://example.com/api/v1/rpc", "http://127.0.0.1/other", "https://user:pass@example.com/api/v1/rpc"} {
		require.Error(t, validateEndpoint(raw))
	}
	require.NoError(t, validateEndpoint("https://example.com/api/v1/rpc"))
}
func TestSignalSelectionAndErrorChannel(t *testing.T) {
	requests := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	s := New(WithEndpoint(server.URL+"/api/v1/rpc"), WithRecipients("+15557654321"))
	require.NoError(t, s.Start())
	s.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, event.TransactionEvent{})
	s.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{})
	select {
	case err := <-s.ErrorChan():
		require.ErrorContains(t, err, "status 503")
	case <-time.After(3 * time.Second):
		t.Fatal("delivery error not reported")
	}
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("selected event not sent")
	}
	select {
	case <-requests:
		t.Fatal("unselected event was sent")
	default:
	}
	require.NoError(t, s.Stop())
}
