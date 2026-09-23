//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTerminalErrorIntegration(t *testing.T) {
	f := &fixture{release: make(chan struct{}), fail: true}
	p := startAdder(t, f, "--output-log-level", "error")
	close(f.release)
	select {
	case <-p.done:
		require.Error(t, p.err)
	case <-time.After(5 * time.Second):
		t.Fatal("Adder did not exit after terminal input failure")
	}
	diagnostics := p.read(t, "stderr.log")
	require.Contains(t, diagnostics, "integration fixture failure")
	require.Contains(t, diagnostics, `"level":"ERROR"`)
	require.Contains(t, diagnostics, "terminal plugin failure, stopping pipeline")
	require.NotContains(t, diagnostics, "DATA RACE")
	require.Empty(t, p.read(t, "stdout.log"))
}
