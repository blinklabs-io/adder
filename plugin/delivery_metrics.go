// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugin

import "time"

// DeliveryMetricsObserver receives bounded webhook delivery measurements.
// Implementations must be safe for concurrent calls.
type DeliveryMetricsObserver interface {
	RecordWebhookAttempt(statusClass string, elapsed time.Duration)
	RecordWebhookOutcome(outcome string)
}
