// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type countingManagedSource struct {
	plugin.Base
	starts  atomic.Int64
	stops   atomic.Int64
	onStart func()
}

func (s *countingManagedSource) Role() plugin.PluginType { return plugin.PluginTypeInput }

func (s *countingManagedSource) StartContext(ctx context.Context) error {
	return s.StartRun(ctx, plugin.BaseConfig{HasOutput: true}, func(context.Context) error {
		s.starts.Add(1)
		if s.onStart != nil {
			s.onStart()
		}
		return nil
	}, plugin.ShutdownHooks{AfterWait: func() error { s.stops.Add(1); return nil }})
}

func (s *countingManagedSource) Stop() error {
	return s.Shutdown(plugin.ShutdownHooks{AfterWait: func() error { s.stops.Add(1); return nil }})
}

func TestManagedPipelineRejectsInitialConfigurationBeforeSourceStart(t *testing.T) {
	for _, invalid := range []string{"missing manifest", "invalid rule"} {
		t.Run(invalid, func(t *testing.T) {
			bundle := newAcceptanceBundle(t)
			if invalid == "invalid rule" {
				customer := localCustomer("u1")
				customer.Filters[0].EventType = "input.not-canonical"
				bundle.publish(t, 1, customer)
			}
			router, err := New(bundle.options)
			require.NoError(t, err)
			source := &countingManagedSource{}
			pipe := pipeline.New()
			pipe.AddInput(source)
			pipe.AddOutput(router)
			require.Error(t, pipe.Start())
			require.Zero(t, source.starts.Load(), "invalid managed output must fail before source setup")
			require.Nil(t, source.OutputChan())
			require.False(t, pipe.IsRunning())
			require.False(t, router.Status().Ready)
			require.Zero(t, router.Status().LastDispatched)
			require.NoError(t, pipe.Stop())
			require.Zero(t, source.starts.Load())
		})
	}
}

func TestManagedPipelineReloadPreservesSourceConnection(t *testing.T) {
	bundle := newAcceptanceBundle(t)
	u1, u2 := localCustomer("u1"), localCustomer("u2")
	bundle.publish(t, 1, u1, u2)
	router, err := New(bundle.options)
	require.NoError(t, err)
	var configuredBeforeSource atomic.Bool
	source := &countingManagedSource{onStart: func() {
		state := router.Status()
		configuredBeforeSource.Store(state.Ready && len(state.Customers) == 2)
	}}
	pipe := pipeline.New()
	pipe.AddInput(source)
	pipe.AddOutput(router)
	require.NoError(t, pipe.Start())
	t.Cleanup(func() { require.NoError(t, pipe.Stop()) })
	require.True(t, configuredBeforeSource.Load())
	continuous := func() {
		t.Helper()
		require.True(t, pipe.IsRunning())
		require.Equal(t, int64(1), source.starts.Load())
		require.Zero(t, source.stops.Load())
	}
	require.True(t, source.Emit(block("before")))
	bundle.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "") == 2 })
	u1.Revision = 2
	u1.Filters[0].EventType = event.TypeTransaction
	bundle.publish(t, 2, u1, u2)
	customerCutover, err := router.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), customerCutover.Cutover)
	continuous()
	require.True(t, source.Emit(block("old-block")))
	bundle.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 2 })
	require.True(t, source.Emit(event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{}, Payload: event.TransactionEvent{BlockHash: "customer-reload"}}))
	bundle.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u1") == 2 })
	u1.Revision = 3
	u1.Filters[0].EventType = event.TypeBlock
	u2.Revision = 2
	u2.Filters[0].EventType = event.TypeTransaction
	bundle.publish(t, 3, u1, u2)
	allCutover, err := router.ReloadAll(context.Background(), 3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), allCutover.Cutover)
	continuous()
	require.True(t, source.Emit(block("all-block")))
	require.True(t, source.Emit(event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{}, Payload: event.TransactionEvent{BlockHash: "all-transaction"}}))
	bundle.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "") == 6 })
	u2.Revision = 3
	u2.Filters[0].EventType = "input.not-canonical"
	bundle.publish(t, 4, u1, u2)
	_, err = router.ReloadAll(context.Background(), 4)
	require.Error(t, err)
	continuous()
	require.True(t, source.Emit(event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{}, Payload: event.TransactionEvent{BlockHash: "after-rejection"}}))
	bundle.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "") == 7 })
	continuous()
	require.NoError(t, pipe.Stop())
	require.Equal(t, int64(1), source.starts.Load())
	require.Equal(t, int64(1), source.stops.Load())
	var actual []string
	for _, record := range bundle.diagnostics.records(t) {
		if record.Action == "submitted_to_plugin" {
			actual = append(actual, fmt.Sprintf("%s/%d/%d", record.Customer, record.Occurrence, record.Revision))
		}
	}
	slices.Sort(actual)
	require.Equal(t, []string{"u1/1/1", "u1/3/2", "u1/4/3", "u2/1/1", "u2/2/1", "u2/5/2", "u2/6/2"}, actual)
	require.Equal(t, []string{"before", "customer-reload", "all-block"}, receiptLabels(t, filepath.Join(bundle.options.LogDir, "u1.jsonl")))
	require.Equal(t, []string{"before", "old-block", "all-transaction", "after-rejection"}, receiptLabels(t, filepath.Join(bundle.options.LogDir, "u2.jsonl")))
}

func BenchmarkManagedDispatchFiveCustomersWithBlockedLane(b *testing.B) {
	root := b.TempDir()
	options := Options{ConfigDir: filepath.Join(root, "config"), CredentialsDir: filepath.Join(root, "credentials"), LogDir: filepath.Join(root, "logs"), Logger: slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn}))}
	for _, path := range []string{options.ConfigDir, options.CredentialsDir, options.LogDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			b.Fatal(err)
		}
	}
	write := func(path string, value any) {
		data, err := json.Marshal(value)
		if err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	manifest := Manifest{Generation: 1}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("u%d", i)
		customer := localCustomer(id)
		customer.Filters = []Rule{{ID: "blocks", EventType: event.TypeBlock, Destination: "all"}, {ID: "transactions", EventType: event.TypeTransaction, Destination: "all"}, {ID: "rollbacks", EventType: event.TypeRollback, Destination: "all"}}
		file := id + ".json"
		write(filepath.Join(options.ConfigDir, file), customer)
		manifest.Customers = append(manifest.Customers, CustomerRef{ID: id, Revision: 1, File: file})
	}
	write(filepath.Join(options.ConfigDir, "manifest.json"), manifest)
	router, err := New(options)
	if err != nil {
		b.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	router.matchGate = func(ctx context.Context, id string) {
		if id == "u1" {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	if err := router.StartContext(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		close(release)
		if err := router.Stop(); err != nil {
			b.Error(err)
		}
	})
	evt := block("benchmark")
	router.dispatch(evt)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		b.Fatal("blocked lane did not enter matcher")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		router.dispatch(evt)
	}
	b.StopTimer()
	status := router.Status()
	blocked := status.Customers["u1"].Counters.InputDropped
	b.ReportMetric(float64(blocked)/float64(b.N), "blocked-drops/op")
	b.ReportMetric(float64(status.Counters.InputDropped-blocked)/float64(b.N), "other-drops/op")
	b.ReportMetric(5, "customers")
	b.ReportMetric(15, "rules")
	// This measures admission with bounded drops and discarded diagnostics, not
	// sustained matching, plugin throughput, or live-mainnet capacity.
}
