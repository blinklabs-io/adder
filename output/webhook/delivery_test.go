// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

type recordingDeliveryMetrics struct {
	mu       sync.Mutex
	attempts map[string]int
	outcomes map[string]int
}

func newRecordingDeliveryMetrics() *recordingDeliveryMetrics {
	return &recordingDeliveryMetrics{
		attempts: make(map[string]int),
		outcomes: make(map[string]int),
	}
}

func (m *recordingDeliveryMetrics) RecordWebhookAttempt(statusClass string, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[statusClass]++
}

func (m *recordingDeliveryMetrics) RecordWebhookOutcome(outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes[outcome]++
}

func (m *recordingDeliveryMetrics) counts() (map[string]int, map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempts := make(map[string]int, len(m.attempts))
	outcomes := make(map[string]int, len(m.outcomes))
	for key, count := range m.attempts {
		attempts[key] = count
	}
	for key, count := range m.outcomes {
		outcomes[key] = count
	}
	return attempts, outcomes
}

func TestDeliveryPolicyAndMetrics(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		retryAfter  string
		retries     int
		wantCalls   int32
		wantOutcome string
	}{
		{"permanent response", http.StatusBadRequest, "", 3, 1, "terminal"},
		{"retry exhausted", http.StatusInternalServerError, "", 2, 3, "exhausted"},
		{"server wait exceeds bound", http.StatusTooManyRequests, "120", 3, 1, "terminal"},
		{"invalid server wait uses backoff", http.StatusTooManyRequests, "invalid", 1, 2, "exhausted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)
			output := New(WithUrl(server.URL, false), WithRetryConfig(tt.retries, time.Millisecond, 10*time.Millisecond))
			observer := newRecordingDeliveryMetrics()
			output.SetDeliveryMetrics(observer)
			require.NoError(t, output.Start())
			t.Cleanup(func() { require.NoError(t, output.Stop()) })
			output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
			require.Eventually(t, func() bool {
				_, outcomes := observer.counts()
				return outcomes["success"]+outcomes["terminal"]+outcomes["exhausted"] == 1
			}, time.Second, time.Millisecond)
			attempts, outcomes := observer.counts()
			require.Equal(t, 1, outcomes[tt.wantOutcome], "unexpected final delivery outcome")
			require.Equal(t, tt.wantCalls, calls.Load())
			require.Equal(t, int(tt.wantCalls), attempts[httpStatusClass(tt.status)])
		})
	}
}

func TestWebhookRedirectsPreservePreviousClientBehavior(t *testing.T) {
	for _, tt := range []struct {
		status     int
		wantMethod string
		wantBody   bool
	}{
		{http.StatusMovedPermanently, http.MethodGet, false},
		{http.StatusFound, http.MethodGet, false},
		{http.StatusSeeOther, http.MethodGet, false},
		{http.StatusTemporaryRedirect, http.MethodPost, true},
		{http.StatusPermanentRedirect, http.MethodPost, true},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			var calls atomic.Int32
			var finalMethod string
			var finalBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/target" {
					finalMethod = r.Method
					finalBody, _ = io.ReadAll(r.Body)
					w.WriteHeader(http.StatusOK)
					return
				}
				http.Redirect(w, r, "/target", tt.status)
			}))
			t.Cleanup(server.Close)
			output := New(WithUrl(server.URL, false))
			observer := newRecordingDeliveryMetrics()
			output.SetDeliveryMetrics(observer)
			err := output.SendWebhook(&event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}})
			require.NoError(t, err)
			require.Equal(t, int32(2), calls.Load())
			require.Equal(t, tt.wantMethod, finalMethod)
			require.Equal(t, tt.wantBody, len(finalBody) > 0)
			attempts, outcomes := observer.counts()
			require.Equal(t, 1, attempts["2xx"])
			require.Equal(t, 1, outcomes["success"])
		})
	}
}

func TestRetryAfterDelay(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		value   string
		valid   bool
		exceeds bool
	}{
		{"2", true, false},
		{"999999999999999999999999999999999", true, true},
		{now.Add(3 * time.Second).Format(http.TimeFormat), true, false},
		{"bad", false, false},
	} {
		_, valid, exceeds := retryAfterDelay(tt.value, now, 5*time.Second)
		require.Equal(t, tt.valid, valid, tt.value)
		require.Equal(t, tt.exceeds, exceeds, tt.value)
	}
}

func TestCertificateFailureIsTerminal(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	output := New(WithUrl(server.URL, false), WithRetryConfig(3, time.Millisecond, 10*time.Millisecond))
	observer := newRecordingDeliveryMetrics()
	output.SetDeliveryMetrics(observer)
	require.NoError(t, output.Start())
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
	require.Eventually(t, func() bool {
		_, outcomes := observer.counts()
		return outcomes["terminal"] == 1
	}, time.Second, time.Millisecond)
	attempts, _ := observer.counts()
	require.Equal(t, 1, attempts["network"])
}

func TestPermanentResponseOnLastAttemptIsTerminal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	output := New(WithUrl(server.URL, false), WithRetryConfig(1, time.Millisecond, 10*time.Millisecond))
	observer := newRecordingDeliveryMetrics()
	output.SetDeliveryMetrics(observer)
	require.NoError(t, output.Start())
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
	require.Eventually(t, func() bool {
		_, outcomes := observer.counts()
		return outcomes["terminal"] == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, int32(2), calls.Load())
	_, outcomes := observer.counts()
	require.Zero(t, outcomes["exhausted"])
}

func TestResponseBodyReadErrorDoesNotRetrySuccessfulDelivery(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(server.Close)
	output := New(WithUrl(server.URL, false), WithRetryConfig(2, time.Millisecond, 10*time.Millisecond))
	observer := newRecordingDeliveryMetrics()
	output.SetDeliveryMetrics(observer)
	require.NoError(t, output.Start())
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
	require.Eventually(t, func() bool {
		_, outcomes := observer.counts()
		return outcomes["success"] == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, int32(1), calls.Load())
}

func TestInvalidEventCountsTerminalFailureWhenErrorChannelIsFull(t *testing.T) {
	output := New()
	observer := newRecordingDeliveryMetrics()
	output.SetDeliveryMetrics(observer)
	require.NoError(t, output.Start())
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	for output.TrySendError(errors.New("fill")) {
	}
	output.InputChan() <- event.Event{Type: event.TypeRollback}
	require.Eventually(t, func() bool {
		_, outcomes := observer.counts()
		return outcomes["terminal"] == 1
	}, time.Second, time.Millisecond)
}
