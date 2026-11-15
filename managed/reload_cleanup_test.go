// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedCanceledFinalSwitchJoinsStagedReplacement(t *testing.T) {
	for _, mode := range []string{"all", "customer"} {
		t.Run(mode, func(t *testing.T) {
			b := newAcceptanceBundle(t)
			b.options.MaxInstances = 2
			customer := localCustomer("u1")
			b.publish(t, 1, customer)
			r := b.start(t, nil)
			baseline := r.Status()
			capacity := r.capacity.Load()
			require.Equal(t, int64(1), baseline.PluginInstances)
			customer.Revision = 2
			customer.Destinations[0].Options["path"] = "u1.replacement.jsonl"
			b.publish(t, 2, customer)
			reload := func(ctx context.Context) (ReloadResult, error) {
				if mode == "customer" {
					return r.ReloadCustomer(ctx, "u1", 2)
				}
				return r.ReloadAll(ctx, 2)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stagedInstances int64
			var hookCalled bool
			r.beforeSwitch = func() {
				hookCalled = true
				stagedInstances = r.Status().PluginInstances
				cancel()
			}
			_, err := reload(ctx)
			require.ErrorContains(t, err, "reload deadline exceeded")
			require.True(t, hookCalled)
			require.Equal(t, int64(2), stagedInstances, "replacement must be live before request cancellation")
			state := r.Status()
			require.True(t, state.Ready)
			require.Equal(t, uint64(1), state.Customers["u1"].Revision)
			require.Equal(t, uint64(1), state.AppliedGeneration)
			require.Equal(t, uint64(2), state.ObservedGeneration)
			require.Equal(t, int64(1), state.PluginInstances, "failed reload must join staged worker cleanup before returning")
			require.Equal(t, capacity, r.capacity.Load())
			require.Equal(t, baseline.RetainedSize, state.RetainedSize)
			r.beforeSwitch = nil
			result, err := reload(context.Background())
			require.NoError(t, err, "retry must have the instance budget released by failed staging")
			require.Equal(t, uint64(2), result.Revisions["u1"])
			require.Equal(t, uint64(2), r.Status().Customers["u1"].Revision)
			r.dispatch(block("after-clean-retry"))
			require.NoError(t, r.Stop())
			require.Empty(t, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
			require.Equal(t, []string{"after-clean-retry"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.replacement.jsonl")))
			require.Zero(t, r.Status().PluginInstances)
			require.Zero(t, r.capacity.Load())
		})
	}
}

func TestManagedConfiguredDiagnosticPrecedesMatchesAcrossReload(t *testing.T) {
	b := newAcceptanceBundle(t)
	customer := localCustomer("u1")
	b.publish(t, 1, customer)
	r := b.start(t, nil)
	customer.Revision = 2
	customer.Filters[0].ID = "replacement-blocks"
	b.publish(t, 2, customer)
	staged, proceed := make(chan struct{}), make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(proceed) }) })
	r.beforeSwitch = func() {
		close(staged)
		<-proceed
	}
	reloaded := make(chan error, 1)
	go func() {
		_, err := r.ReloadAll(context.Background(), 2)
		reloaded <- err
	}()
	select {
	case <-staged:
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not reach final switch barrier")
	}
	r.dispatch(block("before-cutover"))
	dispatched := make(chan struct{})
	go func() {
		<-proceed
		r.dispatch(block("racing-cutover"))
		close(dispatched)
	}()
	release.Do(func() { close(proceed) })
	select {
	case err := <-reloaded:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not finish")
	}
	select {
	case <-dispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent dispatcher did not finish")
	}
	r.beforeSwitch = nil
	r.dispatch(block("after-cutover"))
	require.NoError(t, r.Stop())
	records := b.diagnostics.records(t)
	configured := map[uint64]int{}
	matched := map[uint64]routeRecord{}
	for index, record := range records {
		if record.Customer != "u1" {
			continue
		}
		switch record.Action {
		case "filter_configured":
			_, duplicate := configured[record.Revision]
			require.False(t, duplicate)
			configured[record.Revision] = index
		case "filter_matched":
			configuredAt, exists := configured[record.Revision]
			require.True(t, exists, "matched revision %d before its configuration diagnostic", record.Revision)
			require.Less(t, configuredAt, index)
			_, duplicate := matched[record.Occurrence]
			require.False(t, duplicate)
			matched[record.Occurrence] = record
		}
	}
	require.Len(t, configured, 2)
	require.Len(t, matched, 3)
	require.Equal(t, uint64(1), matched[1].Revision)
	require.Equal(t, "blocks", matched[1].Rule)
	// Either lock winner is valid; each resulting revision must name its own rule.
	switch matched[2].Revision {
	case 1:
		require.Equal(t, "blocks", matched[2].Rule)
	case 2:
		require.Equal(t, "replacement-blocks", matched[2].Rule)
	default:
		t.Fatalf("unexpected revision for the racing event: %d", matched[2].Revision)
	}
	require.Equal(t, uint64(2), matched[3].Revision)
	require.Equal(t, "replacement-blocks", matched[3].Rule)
	require.Equal(t, []string{"before-cutover", "racing-cutover", "after-cutover"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
}
