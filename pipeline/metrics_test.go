// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	eventfilter "github.com/blinklabs-io/adder/filter/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func scrapeMetrics(t *testing.T, p *Pipeline) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	p.MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, recorder.Code)
	return recorder.Body.String()
}

func TestMetricsTrackDeliveryAndFilterDrops(t *testing.T) {
	p := New()
	in := &fakeInput{}
	out := newCollectOutput(2)
	p.AddInput(in)
	p.AddFilter(eventfilter.New(eventfilter.WithTypes([]string{"kept"})))
	p.AddOutput(out)
	require.NoError(t, p.Start())
	t.Cleanup(func() { require.NoError(t, p.Stop()) })

	in.emit(event.Event{Type: "private-address-from-input"})
	require.Eventually(t, func() bool {
		body := scrapeMetrics(t, p)
		return strings.Contains(body, "adder_pipeline_filtered_events_total 1\n") &&
			strings.Contains(body, "adder_pipeline_processed_events_total 0\n") &&
			strings.Contains(body, "adder_pipeline_received_events_total 1\n")
	}, time.Second, time.Millisecond)
	in.emit(event.Event{Type: "kept"})
	require.Equal(t, "kept", out.next(t).Type)
	require.Eventually(t, func() bool {
		body := scrapeMetrics(t, p)
		return strings.Contains(body, "adder_pipeline_received_events_total 2\n") &&
			strings.Contains(body, "adder_pipeline_filtered_events_total 1\n") &&
			strings.Contains(body, "adder_pipeline_processed_events_total 1\n") &&
			strings.Contains(body, "adder_pipeline_processing_duration_seconds_count 1\n") &&
			!strings.Contains(body, "private-address-from-input")
	}, time.Second, time.Millisecond)
}

type heldFilter struct {
	plugin.Base
	entered chan struct{}
	release chan struct{}
}

func (*heldFilter) Role() plugin.PluginType { return plugin.PluginTypeFilter }

func (f *heldFilter) StartContext(ctx context.Context) error {
	return f.StartRun(ctx, plugin.BaseConfig{HasInput: true, HasOutput: true},
		func(ctx context.Context) error {
			input := f.Input()
			f.Go(func() {
				select {
				case evt := <-input:
					close(f.entered)
					select {
					case <-f.release:
						f.Emit(evt)
					case <-ctx.Done():
					}
				case <-ctx.Done():
				}
			})
			return nil
		}, plugin.ShutdownHooks{})
}

func (f *heldFilter) Stop() error { return f.Shutdown(plugin.ShutdownHooks{}) }

func TestMetricsProcessingDurationIncludesFilterWait(t *testing.T) {
	p := New()
	in := &fakeInput{}
	filter := &heldFilter{entered: make(chan struct{}), release: make(chan struct{})}
	out := newCollectOutput(1)
	p.AddInput(in)
	p.AddFilter(filter)
	p.AddOutput(out)
	require.NoError(t, p.Start())
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	t.Cleanup(func() {
		select {
		case <-filter.release:
		default:
			close(filter.release)
		}
	})

	in.emit(event.Event{Type: "held"})
	select {
	case <-filter.entered:
	case <-time.After(time.Second):
		t.Fatal("filter did not receive event")
	}
	require.Contains(t, scrapeMetrics(t, p),
		"adder_pipeline_processing_duration_seconds_count 0\n")
	// Hold the event longer than the 10 ms bucket before releasing the filter.
	<-time.After(30 * time.Millisecond)
	close(filter.release)
	require.Equal(t, "held", out.next(t).Type)
	require.Eventually(t, func() bool {
		body := scrapeMetrics(t, p)
		return strings.Contains(body, "adder_pipeline_processing_duration_seconds_count 1\n") &&
			strings.Contains(body, "adder_pipeline_processing_duration_seconds_bucket{le=\"0.01\"} 0\n")
	}, time.Second, time.Millisecond)
}

type gatedInput struct {
	fakeInput
	entered chan struct{}
	release chan struct{}
}

func (g *gatedInput) StartContext(ctx context.Context) error {
	close(g.entered)
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return g.fakeInput.StartContext(ctx)
}

func TestMetricsScrapeWhileInputStarts(t *testing.T) {
	p := New()
	in := &gatedInput{entered: make(chan struct{}), release: make(chan struct{})}
	p.AddInput(in)
	started := make(chan error, 1)
	go func() { started <- p.Start() }()
	<-in.entered
	defer func() {
		select {
		case <-in.release:
		default:
			close(in.release)
		}
	}()

	response := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		p.MetricsHandler().ServeHTTP(recorder,
			httptest.NewRequest("GET", "/metrics", nil))
		response <- recorder.Code
	}()
	select {
	case code := <-response:
		require.Equal(t, 200, code)
	case <-time.After(time.Second):
		t.Fatal("metrics scrape blocked on input startup")
	}
	close(in.release)
	require.NoError(t, <-started)
	require.NoError(t, p.Stop())
}
