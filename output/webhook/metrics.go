// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type deliveryMetrics struct {
	attempts *prometheus.CounterVec
	outcomes *prometheus.CounterVec
	latency  prometheus.Histogram
}

func newDeliveryMetrics() *deliveryMetrics {
	return &deliveryMetrics{
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "adder_webhook_http_attempts_total",
			Help: "HTTP delivery attempts by response status class; network means no response.",
		}, []string{"status_class"}),
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "adder_webhook_delivery_outcomes_total",
			Help: "Final event outcomes: success, terminal response or error, or exhausted retries.",
		}, []string{"outcome"}),
		latency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "adder_webhook_http_attempt_duration_seconds",
			Help:    "Time from starting an HTTP attempt until its response body is consumed or the attempt fails.",
			Buckets: []float64{0.001, 0.01, 0.1, 1, 5, 10},
		}),
	}
}

func httpStatusClass(status int) string {
	if status < 100 || status >= 600 {
		return "other"
	}
	return fmt.Sprintf("%dxx", status/100)
}

func (m *deliveryMetrics) observeAttempt(statusClass string, elapsed time.Duration) {
	m.attempts.WithLabelValues(statusClass).Inc()
	m.latency.Observe(elapsed.Seconds())
}

// MetricsCollectors lets the owning pipeline include delivery metrics in /metrics.
func (w *WebhookOutput) MetricsCollectors() []prometheus.Collector {
	return []prometheus.Collector{w.metrics.attempts, w.metrics.outcomes, w.metrics.latency}
}
