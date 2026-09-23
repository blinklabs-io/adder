//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReconnectIntegration(t *testing.T) {
	f := &fixture{release: make(chan struct{}), failFirst: 1}
	p := startAdder(t, f, "--input-utxorpc-auto-reconnect=true", "--output-log-format", "json")
	close(f.release)
	want := []string{"input.block", "input.transaction", "input.governance", "input.rollback"}
	require.Equal(t, want, p.events(t, len(want)))
	p.waitOutput(t, "stdout.log", len(want))
	p.healthy(t)
	p.stop(t, syscall.SIGTERM)
	require.EqualValues(t, 2, f.attempts.Load(), "one failed stream followed by one successful stream")
	checkJSON(t, p.read(t, "stdout.log"), want)
	require.Contains(t, p.read(t, "stderr.log"), "utxorpc stream ended, reconnecting")
	require.Contains(t, p.read(t, "stderr.log"), "integration fixture failure")
	require.NotContains(t, p.read(t, "stderr.log"), "terminal plugin failure")
}
