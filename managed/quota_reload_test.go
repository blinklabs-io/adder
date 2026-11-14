// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedRetryAfterCanOutlastActiveSendBudget(t *testing.T) {
	var attempts atomic.Int64
	receipts := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		receipts <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	b := newAcceptanceBundle(t)
	b.options.SendTimeout = 1500 * time.Millisecond
	c := localCustomer("u1")
	c.Destinations = []Destination{b.webhook(t, "u1", "remote", server.URL, 1)}
	b.publish(t, 1, c)
	r := b.start(t, nil)
	started := time.Now()
	r.dispatch(block("retry-after"))
	select {
	case <-receipts:
	case <-time.After(5 * time.Second):
		t.Fatal("provider pause stopped delivery", r.Status())
	}
	require.GreaterOrEqual(t, time.Since(started), 2*time.Second)
	require.Equal(t, int64(2), attempts.Load())
	require.NoError(t, r.Stop())
	require.Zero(t, countAction(b.diagnostics.records(t), "submission_uncertain", "u1"))
}

func TestManagedQuotaWaitDoesNotRevokeDestination(t *testing.T) {
	receipts := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		receipts <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	b := newAcceptanceBundle(t)
	b.options.SendTimeout = 100 * time.Millisecond
	c := localCustomer("u1")
	c.Destinations = []Destination{b.webhook(t, "u1", "remote", server.URL, 1)}
	c.Destinations[0].IntervalMS = 300
	b.publish(t, 1, c)
	r := b.start(t, nil)
	for _, label := range []string{"first", "second", "third"} {
		r.dispatch(block(label))
		select {
		case <-receipts:
		case <-time.After(3 * time.Second):
			t.Fatal("normal quota waiting stopped delivery", r.Status())
		}
	}
	require.NoError(t, r.Stop())
	require.Zero(t, r.Status().Counters.Canceled)
	require.Zero(t, r.Status().Counters.PluginErrors)
	require.Zero(t, countAction(b.diagnostics.records(t), "submission_uncertain", "u1"))
}

func TestManagedQuotaIntervalChangesAtCutover(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	c.Destinations = []Destination{b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/private", 1)}
	b.publish(t, 1, c)
	r := b.start(t, nil)
	group := r.groups[c.Destinations[0].LimitGroup]
	if group == nil {
		t.Fatal("missing configured limit group")
		return
	}
	until := time.Now().Add(time.Minute)
	group.mu.Lock()
	group.next = until
	group.mu.Unlock()
	c.Revision = 2
	c.Destinations[0].IntervalMS = 1000
	b.publish(t, 2, c)
	r.beforeSwitch = func() {
		group.mu.Lock()
		defer group.mu.Unlock()
		require.Equal(t, time.Millisecond, group.interval, "staging must leave the active quota intact")
	}
	_, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	group.mu.Lock()
	require.Equal(t, time.Second, group.interval)
	require.Equal(t, until, group.next, "reload must preserve pending Retry-After")
	group.mu.Unlock()
}

func TestManagedQuotaConflictLeavesOtherCustomerUnchanged(t *testing.T) {
	b := newAcceptanceBundle(t)
	u1, u2 := localCustomer("u1"), localCustomer("u2")
	u1.Destinations = []Destination{b.webhook(t, "u1", "remote", "http://127.0.0.1:43210/one", 1)}
	u2.Destinations = []Destination{b.webhook(t, "u2", "remote", "http://127.0.0.1:43210/two", 1)}
	b.publish(t, 1, u1, u2)
	r := b.start(t, nil)
	u1.Revision = 2
	u1.Destinations[0].IntervalMS = 1000
	b.publish(t, 2, u1, u2)
	_, err := r.ReloadCustomer(t.Context(), "u1", 2)
	require.ErrorContains(t, err, "interval mismatch")
	require.Equal(t, uint64(1), r.Status().Customers["u1"].Revision)
	require.Equal(t, uint64(1), r.Status().Customers["u2"].Revision)
	u2.Revision = 2
	u2.Destinations[0].IntervalMS = 1000
	b.publish(t, 3, u1, u2)
	_, err = r.ReloadAll(t.Context(), 3)
	require.NoError(t, err)
	require.Equal(t, uint64(2), r.Status().Customers["u1"].Revision)
	require.Equal(t, uint64(2), r.Status().Customers["u2"].Revision)
}
