// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	received        prometheus.Counter
	processed       prometheus.Counter
	filtered        prometheus.Counter
	processing      prometheus.Histogram
	webhookAttempts *prometheus.CounterVec
	webhookOutcomes *prometheus.CounterVec
	webhookLatency  prometheus.Histogram
}

func newMetrics() *metrics {
	return &metrics{
		received: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "adder_pipeline_received_events_total",
			Help: "Events read from input plugin output channels by the pipeline.",
		}),
		processed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "adder_pipeline_processed_events_total",
			Help: "Events completed by the terminal stage after all configured output channels accept them.",
		}),
		filtered: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "adder_pipeline_filtered_events_total",
			Help: "Filter drops reported through SetPipelineDropObserver; built-in filters report each rejected event.",
		}),
		processing: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "adder_pipeline_processing_duration_seconds",
			Help:    "Time from reading an input event to acceptance by all configured output channels; completed events retaining pipeline ingress time are observed.",
			Buckets: []float64{0.001, 0.01, 0.1, 1, 10, 60},
		}),
		webhookAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "adder_webhook_http_attempts_total",
			Help: "HTTP delivery attempts by response status class; network means no response.",
		}, []string{"status_class"}),
		webhookOutcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "adder_webhook_delivery_outcomes_total",
			Help: "Final event outcomes: success, terminal response or error, or exhausted retries.",
		}, []string{"outcome"}),
		webhookLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "adder_webhook_http_attempt_duration_seconds",
			Help:    "Time from starting an HTTP attempt until its response body is consumed or the attempt fails.",
			Buckets: []float64{0.001, 0.01, 0.1, 1, 5, 10},
		}),
	}
}

func (m *metrics) RecordWebhookAttempt(
	statusClass string,
	elapsed time.Duration,
) {
	m.webhookAttempts.WithLabelValues(statusClass).Inc()
	m.webhookLatency.Observe(elapsed.Seconds())
}

func (m *metrics) RecordWebhookOutcome(outcome string) {
	m.webhookOutcomes.WithLabelValues(outcome).Inc()
}

// MetricsHandler exposes this pipeline's metrics in Prometheus format.
func (p *Pipeline) MetricsHandler() http.Handler {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		p.metrics.received,
		p.metrics.processed,
		p.metrics.filtered,
		p.metrics.processing,
		p.metrics.webhookAttempts,
		p.metrics.webhookOutcomes,
		p.metrics.webhookLatency,
	)
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}
