//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fetchMetrics(client *http.Client, p *process) (string, error) {
	response, err := client.Get(p.apiURL + "/metrics")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metrics HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	return string(body), err
}

func TestMetricsIntegration(t *testing.T) {
	f := &fixture{release: make(chan struct{})}
	p := startAdder(t, f, "--filter-type", "input.transaction,input.rollback")
	close(f.release)
	require.Equal(t, []string{"input.transaction", "input.rollback"}, p.events(t, 2))

	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		metrics, err := fetchMetrics(client, p)
		if err != nil {
			return false
		}
		return strings.Contains(metrics, "adder_pipeline_processed_events_total 2\n") &&
			strings.Contains(metrics, "adder_pipeline_filtered_events_total 2\n") &&
			strings.Contains(metrics, "adder_pipeline_processing_duration_seconds_count 2\n")
	}, 5*time.Second, 20*time.Millisecond)
	p.stop(t, syscall.SIGTERM)
}
