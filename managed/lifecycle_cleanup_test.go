// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type blockedShutdownPlugin struct {
	plugin.Base
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockedShutdownPlugin) Role() plugin.PluginType { return plugin.PluginTypeOutput }

func (p *blockedShutdownPlugin) StartContext(ctx context.Context) error {
	return p.StartRun(ctx, plugin.BaseConfig{HasInput: true}, func(context.Context) error {
		return nil
	}, plugin.ShutdownHooks{})
}

func (p *blockedShutdownPlugin) Stop() error {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return p.Shutdown(plugin.ShutdownHooks{})
}

func TestManagedShutdownRetainsBlockedPluginOwnershipUntilCleanup(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.ShutdownTimeout = 25 * time.Millisecond
	b.publish(t, 1, localCustomer("u1"))
	r := b.start(t, nil)
	p := &blockedShutdownPlugin{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(r.runCtx)
	require.NoError(t, p.StartContext(ctx))
	const weight = int64(256)
	require.True(t, r.reserveCapacity(r.destinationCapacity()))
	require.True(t, r.reserve(weight))
	r.instances.Add(1)
	d := &destination{
		router: r, customer: "u1", plugin: p, pluginErrors: p.ErrorChan(),
		spec:  preparedDestination{config: Destination{ID: "blocked", Plugin: "log"}},
		queue: make(chan submission, r.options.DestinationQueue), completed: make(chan struct{}, 1),
		done: make(chan struct{}), ctx: ctx, cancel: cancel, weight: weight,
	}
	d.refs.Store(1)
	r.mu.Lock()
	lane := r.lanes["u1"]
	if lane == nil || lane.active == nil {
		r.mu.Unlock()
		t.Fatal("missing configured customer lane")
		return
	}
	lane.active.destinations["blocked"] = d
	r.mu.Unlock()
	r.workers.Go(d.run)
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(p.release) })
		r.mu.Lock()
		done := r.cleanupDone
		r.mu.Unlock()
		if done != nil {
			awaitDiagnosticSignal(t, done, "late router cleanup")
		}
	})
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop() }()
	awaitDiagnosticSignal(t, p.entered, "plugin blocked in Stop")
	select {
	case err := <-stopped:
		require.ErrorIs(t, err, errCleanupPending)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked plugin made managed shutdown unbounded")
	}
	state := r.Status()
	require.False(t, state.Ready)
	require.Equal(t, int64(2), state.PluginInstances, "active configurations still own both destination instances")
	require.GreaterOrEqual(t, state.RetainedSize, weight)
	require.GreaterOrEqual(t, r.capacity.Load(), r.destinationCapacity())
	require.False(t, d.stopped.Load())
	done, runCtx := r.cleanupDone, r.runCtx
	select {
	case <-done:
		t.Fatal("cleanup completed before the blocked plugin was joined")
	default:
	}
	require.ErrorIs(t, r.Stop(), errCleanupPending, "repeated Stop must not claim unfinished cleanup succeeded")
	for range 2 {
		restarted := make(chan error, 1)
		go func() { restarted <- r.StartContext(context.Background()) }()
		select {
		case err := <-restarted:
			require.ErrorIs(t, err, errCleanupPending)
		case <-time.After(2 * time.Second):
			t.Fatal("restart waited indefinitely for the previous plugin")
		}
		require.Equal(t, done, r.cleanupDone)
		require.True(t, runCtx == r.runCtx)
		require.Equal(t, state.PluginInstances, r.Status().PluginInstances)
		require.GreaterOrEqual(t, r.Status().RetainedSize, weight)
	}
	release.Do(func() { close(p.release) })
	awaitDiagnosticSignal(t, done, "router cleanup after plugin release")
	awaitDiagnosticSignal(t, r.diagnosticsDone, "diagnostic cleanup after plugin release")
	require.True(t, d.stopped.Load())
	require.Zero(t, r.Status().PluginInstances)
	require.Zero(t, r.Status().RetainedSize)
	require.Zero(t, r.capacity.Load())
	require.NoError(t, r.Stop())
	require.NoError(t, r.StartContext(context.Background()))
	require.True(t, r.Status().Ready)
	require.Equal(t, int64(1), r.Status().PluginInstances)
	r.dispatch(block("after-blocked-plugin-cleanup"))
	require.NoError(t, r.Stop())
	require.Equal(t, []string{"after-blocked-plugin-cleanup"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
}

func TestManagedMatchedRuleMetadataConsumesDestinationBudget(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.MaxRules = 64
	b.options.MaxQueueRetainedSize = 4 << 10
	customer := localCustomer("u1")
	customer.Filters = nil
	for n := range 64 {
		customer.Filters = append(customer.Filters, Rule{
			ID:        fmt.Sprintf("rule-%02d-", n) + strings.Repeat("x", 56),
			EventType: event.TypeBlock, Destination: "all",
		})
	}
	b.publish(t, 1, customer)
	r := b.start(t, nil)
	r.dispatch(block("small"))
	b.diagnostics.await(t, func(records []routeRecord) bool {
		return countAction(records, "destination_dropped", "u1") == 1
	})
	state := r.Status()
	require.Equal(t, uint64(64), state.Counters.Matched)
	require.Zero(t, state.Counters.InputDropped, "the event payload fits in the customer input budget")
	require.Equal(t, uint64(1), state.Counters.DestinationDropped, "64 retained rule IDs alone exceed the 4 KiB destination budget")
	require.Zero(t, state.Counters.SubmittedToPlugin)
	require.NoError(t, r.Stop())
	require.Empty(t, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
	require.Zero(t, r.Status().RetainedSize)
	require.Zero(t, r.capacity.Load())
}
