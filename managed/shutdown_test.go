// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedShutdownDeadlineCancelsAcceptedWork(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.ShutdownTimeout = 50 * time.Millisecond
	b.publish(t, 1, localCustomer("u1"))
	entered := make(chan struct{})
	var once sync.Once
	r := b.start(t, func(r *Router) {
		r.matchGate = func(ctx context.Context, _ string) {
			once.Do(func() { close(entered) })
			<-ctx.Done()
		}
	})
	r.dispatch(block("in-worker"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not enter blocked match")
	}
	r.dispatch(block("queued-one"))
	r.dispatch(block("queued-two"))
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel blocked matching at its deadline")
	}
	status := r.Status()
	require.False(t, status.Ready)
	require.Equal(t, uint64(3), status.Counters.Canceled)
	require.Zero(t, status.Counters.SubmittedToPlugin)
	require.Zero(t, status.PluginInstances)
	require.Eventually(t, func() bool { return r.Status().RetainedSize == 0 && r.capacity.Load() == 0 }, time.Second, time.Millisecond)
	b.diagnostics.await(t, func(records []routeRecord) bool {
		return countAction(records, "input_canceled", "u1") == 3
	})
	r.dispatch(block("after-stop"))
	require.Equal(t, status.LastDispatched, r.Status().LastDispatched)
}

func TestManagedQueueBudgetsProtectAnIndependentLane(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.InputQueue = 8
	b.options.DestinationQueue = 1
	b.options.MaxEventSize = 4 << 10
	b.options.MaxRetainedSize = 800 << 10
	u1, u2 := localCustomer("u1"), localCustomer("u2")
	b.publish(t, 1, u1, u2)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := b.start(t, func(r *Router) {
		r.matchGate = func(ctx context.Context, id string) {
			if id == "u1" {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
		}
	})
	large := block(strings.Repeat("x", 3500))
	r.dispatch(large)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow lane did not enter matcher")
	}
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 1 })
	for n := 2; n <= 9; n++ {
		r.dispatch(large)
		b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == n })
	}
	b.publish(t, 2, u1, u2, localCustomer("u3"))
	_, err := r.ReloadAll(context.Background(), 2)
	require.ErrorContains(t, err, "budget", "reload must reserve worst-case queue capacity before adding a lane")
	require.Len(t, r.Status().Customers, 2)
	r.dispatch(large)
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 10 })
	status := r.Status()
	require.Equal(t, uint64(1), status.Customers["u1"].Counters.InputDropped)
	require.Zero(t, status.Customers["u2"].Counters.InputDropped)
	require.LessOrEqual(t, status.RetainedSize, b.options.MaxRetainedSize)
	require.LessOrEqual(t, r.capacity.Load(), b.options.MaxRetainedSize)
	close(release)
	require.NoError(t, r.Stop())
	require.Len(t, receiptLabels(t, filepath.Join(b.options.LogDir, "u2.jsonl")), 10)
	require.Zero(t, r.capacity.Load())
}

func TestManagedInsufficientQueueBudgetFailsStartup(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.InputQueue = 8
	b.options.DestinationQueue = 1
	b.options.MaxEventSize = 4 << 10
	b.options.MaxRetainedSize = 100 << 10
	b.publish(t, 1, localCustomer("u1"))
	r, err := New(b.options)
	require.NoError(t, err)
	require.ErrorContains(t, r.StartContext(context.Background()), "budget")
	require.NoError(t, r.Stop())
	require.False(t, r.Status().Ready)
	require.Zero(t, r.Status().PluginInstances)
	require.Zero(t, r.capacity.Load())
}
