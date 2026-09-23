//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilteringIntegration(t *testing.T) {
	want := []string{"input.transaction", "input.rollback"}
	f := &fixture{release: make(chan struct{})}
	p := startAdder(t, f, "--output-log-format", "json", "--filter-type", "input.transaction,input.rollback")
	close(f.release)
	// Rollback is the last fixture event; receiving it proves the
	// preceding rejected events traversed the filter already.
	require.Equal(t, want, p.events(t, len(want)))
	p.waitOutput(t, "stdout.log", len(want))
	p.healthy(t)
	p.stop(t, syscall.SIGTERM)
	checkJSON(t, p.read(t, "stdout.log"), want)
}
