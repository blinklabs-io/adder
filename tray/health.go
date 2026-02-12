// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tray

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	healthPollInterval       = 10 * time.Second
	healthHTTPTimeout        = 5 * time.Second
	healthConsecutiveFailCap = 3
)

// HealthResponse represents the JSON response from the adder
// healthcheck endpoint.
type HealthResponse struct {
	Failed bool   `json:"failed"`
	Reason string `json:"reason,omitempty"`
}

// HealthPoller periodically checks the adder HTTP healthcheck
// endpoint and updates a StatusTracker accordingly.
type HealthPoller struct {
	address             string
	port                uint
	tracker             *StatusTracker
	client              *http.Client
	stopCh              chan struct{}
	consecutiveFailures int
}

// NewHealthPoller creates a new HealthPoller that will poll the given
// address and port, updating the provided StatusTracker.
func NewHealthPoller(
	address string,
	port uint,
	tracker *StatusTracker,
) *HealthPoller {
	return &HealthPoller{
		address: address,
		port:    port,
		tracker: tracker,
		client: &http.Client{
			Timeout: healthHTTPTimeout,
		},
		stopCh: make(chan struct{}),
	}
}

// Start begins periodic health polling in a background goroutine.
func (hp *HealthPoller) Start() {
	go hp.run()
}

// Stop signals the health poller to stop. It is safe to call
// multiple times.
func (hp *HealthPoller) Stop() {
	select {
	case <-hp.stopCh:
	default:
		close(hp.stopCh)
	}
}

func (hp *HealthPoller) run() {
	ticker := time.NewTicker(healthPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hp.stopCh:
			return
		case <-ticker.C:
			hp.poll()
		}
	}
}

func (hp *HealthPoller) poll() {
	resp, err := hp.client.Get(hp.healthURL()) //nolint:noctx // short-lived poller with client timeout
	if err != nil || resp == nil {
		hp.consecutiveFailures++
		slog.Debug(
			"health poll failed",
			"error", err,
			"consecutive_failures", hp.consecutiveFailures,
		)
		if hp.consecutiveFailures >= healthConsecutiveFailCap {
			hp.tracker.Set(StatusError)
		}
		return
	}
	defer resp.Body.Close()

	var hr HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		hp.consecutiveFailures++
		slog.Debug(
			"health poll decode failed",
			"error", err,
			"consecutive_failures", hp.consecutiveFailures,
		)
		if hp.consecutiveFailures >= healthConsecutiveFailCap {
			hp.tracker.Set(StatusError)
		}
		return
	}

	if hr.Failed {
		hp.consecutiveFailures++
		slog.Debug(
			"health poll reported failure",
			"reason", hr.Reason,
			"consecutive_failures", hp.consecutiveFailures,
		)
		if hp.consecutiveFailures >= healthConsecutiveFailCap {
			hp.tracker.Set(StatusError)
		}
		return
	}

	// Healthy response: reset failures and mark connected
	hp.consecutiveFailures = 0
	hp.tracker.Set(StatusConnected)
}

// healthURL returns the full URL of the healthcheck endpoint.
func (hp *HealthPoller) healthURL() string {
	return fmt.Sprintf("http://%s:%d/healthcheck", hp.address, hp.port)
}
