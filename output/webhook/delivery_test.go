// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

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
		{"redirect", http.StatusFound, "", 3, 1, "terminal"},
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
				if tt.status == http.StatusFound {
					w.Header().Set("Location", "/follow")
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)
			output := New(WithUrl(server.URL, false), WithRetryConfig(tt.retries, time.Millisecond, 10*time.Millisecond))
			require.NoError(t, output.Start())
			t.Cleanup(func() { require.NoError(t, output.Stop()) })
			output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
			require.Eventually(t, func() bool {
				return testutil.ToFloat64(output.metrics.outcomes.WithLabelValues("success"))+
					testutil.ToFloat64(output.metrics.outcomes.WithLabelValues("terminal"))+
					testutil.ToFloat64(output.metrics.outcomes.WithLabelValues("exhausted")) == 1
			}, time.Second, time.Millisecond)
			require.Equal(t, float64(1), testutil.ToFloat64(
				output.metrics.outcomes.WithLabelValues(tt.wantOutcome)),
				"unexpected final delivery outcome")
			require.Equal(t, tt.wantCalls, calls.Load())
			require.Equal(t, float64(tt.wantCalls), testutil.ToFloat64(
				output.metrics.attempts.WithLabelValues(httpStatusClass(tt.status))))
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
	require.NoError(t, output.Start())
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(output.metrics.outcomes.WithLabelValues("terminal")) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, float64(1), testutil.ToFloat64(output.metrics.attempts.WithLabelValues("network")))
}

func TestInvalidEventCountsTerminalFailureWhenErrorChannelIsFull(t *testing.T) {
	output := New()
	require.NoError(t, output.Start())
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	for output.TrySendError(errors.New("fill")) {
	}
	output.InputChan() <- event.Event{Type: event.TypeRollback}
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(output.metrics.outcomes.WithLabelValues("terminal")) == 1
	}, time.Second, time.Millisecond)
}
