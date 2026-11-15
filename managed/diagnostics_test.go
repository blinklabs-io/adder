// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type heldDiagnosticSink struct {
	capture *diagnosticCapture
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	unblock sync.Once
}

func newHeldDiagnosticSink(capture *diagnosticCapture) *heldDiagnosticSink {
	return &heldDiagnosticSink{capture: capture, entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *heldDiagnosticSink) Write(data []byte) (int, error) {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	return s.capture.Write(data)
}

func (s *heldDiagnosticSink) free() { s.unblock.Do(func() { close(s.release) }) }

func awaitDiagnosticSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func startWithHeldDiagnostics(t *testing.T, b *acceptanceBundle, configure func(*Router)) (*Router, *heldDiagnosticSink) {
	t.Helper()
	sink := newHeldDiagnosticSink(b.diagnostics)
	b.options.Logger = slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b.options.ShutdownTimeout = 50 * time.Millisecond
	r, err := New(b.options)
	require.NoError(t, err)
	if configure != nil {
		configure(r)
	}
	t.Cleanup(func() {
		sink.free()
		require.NoError(t, r.Stop())
	})
	started := make(chan error, 1)
	go func() { started <- r.StartContext(context.Background()) }()
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("configuration logging blocked initial startup")
	}
	awaitDiagnosticSignal(t, sink.entered, "blocked diagnostic sink")
	return r, sink
}

func stopWithHeldDiagnostics(t *testing.T, r *Router) {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked diagnostic sink prevented bounded shutdown")
	}
}

func TestManagedSlowDiagnosticsDoNotBlockCustomerProcessing(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.publish(t, 1, localCustomer("u1"), localCustomer("u2"))
	slowEntered, fastEntered := make(chan struct{}), make(chan struct{}, 2)
	var once sync.Once
	r, sink := startWithHeldDiagnostics(t, b, func(r *Router) {
		r.matchGate = func(ctx context.Context, customer string) {
			if customer == "u1" {
				once.Do(func() { close(slowEntered) })
				<-ctx.Done()
			} else {
				fastEntered <- struct{}{}
			}
		}
	})
	labels := []string{"diagnostic-private-payload-a", "diagnostic-private-payload-b"}
	for _, label := range labels {
		dispatched := make(chan struct{})
		go func() {
			r.dispatch(block(label))
			close(dispatched)
		}()
		awaitDiagnosticSignal(t, dispatched, "nonblocking dispatcher")
		awaitDiagnosticSignal(t, fastEntered, "independent customer's next event")
	}
	awaitDiagnosticSignal(t, slowEntered, "blocked customer")
	require.Eventually(t, func() bool {
		state := r.Status()
		return state.Customers["u2"].Destinations["local"].Counters.SubmittedToPlugin == 2
	}, 2*time.Second, time.Millisecond)
	stopWithHeldDiagnostics(t, r)
	require.Equal(t, labels, receiptLabels(t, filepath.Join(b.options.LogDir, "u2.jsonl")))
	require.Empty(t, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
	state := r.Status()
	require.Equal(t, uint64(2), state.Counters.Matched)
	require.Equal(t, uint64(2), state.Counters.SubmittedToPlugin)
	require.Equal(t, uint64(2), state.Counters.Canceled)
	require.Zero(t, state.PluginInstances)
	require.Positive(t, state.RetainedSize, "blocked diagnostic cleanup remains charged to the budget")
	require.Empty(t, b.diagnostics.text())
	done := r.diagnosticsDone
	sink.free()
	awaitDiagnosticSignal(t, done, "late diagnostic cleanup")
	require.Zero(t, r.Status().RetainedSize)
	require.Equal(t, 2, countAction(b.diagnostics.records(t), "filter_matched", "u2"))
	require.Equal(t, 2, countAction(b.diagnostics.records(t), "submitted_to_plugin", "u2"))
	for _, label := range labels {
		require.NotContains(t, b.diagnostics.text(), label)
	}
}

func TestManagedDiagnosticQueueBoundMetadataAndRestart(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.publish(t, 1, localCustomer("u1"))
	r, sink := startWithHeldDiagnostics(t, b, nil)
	queue, done := r.diagnostics, r.diagnosticsDone
	require.Equal(t, 1024, cap(queue))
	rules := []string{"blocks"}
	for n := range 1024 {
		r.record("submitted_to_plugin", "u1", "local", uint64(n+1), 1, rules)
	}
	rules[0] = "not-captured-after-enqueue"
	for range 7 {
		r.record("input_dropped", "u1", "", 0, 1, nil)
	}
	require.Len(t, queue, 1024)
	require.Equal(t, uint64(7), r.Status().Counters.DiagnosticDropped)
	recorder := httptest.NewRecorder()
	r.metrics(recorder, httptest.NewRequest("GET", "/metrics", nil))
	require.Contains(t, recorder.Body.String(), "adder_managed_diagnostic_dropped_total 7\n")
	stopWithHeldDiagnostics(t, r)
	require.ErrorContains(t, r.StartContext(context.Background()), "diagnostic cleanup pending")
	select {
	case <-done:
		t.Fatal("blocked sink was incorrectly reported as cleaned up")
	default:
	}
	sink.free()
	awaitDiagnosticSignal(t, done, "bounded queue cleanup")
	records := b.diagnostics.records(t)
	require.Len(t, records, 1025)
	require.Equal(t, 1024, countAction(records, "submitted_to_plugin", "u1"))
	for _, record := range records {
		if record.Action == "submitted_to_plugin" {
			require.Equal(t, []string{"blocks"}, record.Rules)
		}
	}
	require.NotContains(t, b.diagnostics.text(), "not-captured-after-enqueue")
	require.NoError(t, r.StartContext(context.Background()))
	require.NotEqual(t, done, r.diagnosticsDone)
	require.NoError(t, r.Stop())
	require.Zero(t, r.Status().PluginInstances)
	require.Zero(t, r.Status().RetainedSize)
}
