//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoggingIntegration(t *testing.T) {
	for _, tc := range []struct{ output, level string }{
		{"json", "info"},
		{"text", "info"},
		{"file", "debug"},
	} {
		output, level := tc.output, tc.level
		t.Run(output+"/"+level, func(t *testing.T) {
			f := &fixture{release: make(chan struct{})}
			flags := []string{"--output", "log", "--output-log-format", "json", "--output-log-level", level,
				"--input-utxorpc-intersect-point", "100." + strings.Repeat("01", 32)}
			if output == "text" {
				flags = append(flags, "--output-log-format", "text")
			} else if output == "file" {
				flags = append(flags, "--output-log-path", "events.jsonl")
			}
			p := startAdder(t, f, flags...)
			close(f.release)
			wantTypes := []string{"input.block", "input.transaction", "input.governance", "input.rollback"}
			require.Equal(t, wantTypes, p.events(t, 4))
			p.healthy(t)
			// Observer delivery precedes output delivery; wait for the final
			// output record before signaling, since shutdown is not lossless.
			name := "stdout.log"
			if output == "file" {
				name = "events.jsonl"
			}
			p.waitOutput(t, name, 4)
			signal := os.Signal(syscall.SIGTERM)
			if level == "debug" || level == "warn" {
				signal = os.Interrupt
			}
			p.stop(t, signal)
			stdout := p.read(t, "stdout.log")
			switch output {
			case "file":
				require.Empty(t, stdout)
				checkJSON(t, p.read(t, "events.jsonl"), wantTypes)
			case "json":
				checkJSON(t, stdout, wantTypes)
			case "text":
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				require.Len(t, lines, 4)
				for i, label := range []string{"BLOCK", "TX", "GOVERNANCE", "ROLLBACK"} {
					require.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} `+label+`\s+slot=100\s`, lines[i])
				}
			}
			diagnostics := p.read(t, "stderr.log")
			require.Equal(t, level == "debug" || level == "info", strings.Contains(diagnostics, "started utxorpc input"))
			require.Equal(t, level != "error", strings.Contains(diagnostics, "intersect-point is set, overriding intersect-tip to false"))
			for _, line := range strings.Split(strings.TrimSpace(diagnostics), "\n") {
				if line == "" {
					continue
				}
				var record struct {
					Level string `json:"level"`
				}
				require.NoError(t, json.Unmarshal([]byte(line), &record))
				require.NotEqual(t, "ERROR", record.Level, line)
			}
		})
	}
}

func TestOutputLevelIntegration(t *testing.T) {
	f := &fixture{release: make(chan struct{})}
	p := startAdder(t, f, "--output-log-format", "json",
		"--output-log-level", "error")
	close(f.release)
	want := []string{"input.block", "input.transaction", "input.governance", "input.rollback"}
	require.Equal(t, want, p.events(t, len(want)))
	p.healthy(t)
	p.stop(t, syscall.SIGTERM)
	require.Empty(t, p.read(t, "stdout.log"))
	require.Empty(t, p.read(t, "stderr.log"))
	require.NotContains(t, p.read(t, "stderr.log"), `"level":"ERROR"`)
}

func checkJSON(t *testing.T, output string, wantTypes []string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	require.Len(t, lines, len(wantTypes))
	for i, line := range lines {
		var evt map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &evt))
		require.Equal(t, wantTypes[i], evt["type"])
		require.NotEmpty(t, evt["timestamp"])
		require.NotEmpty(t, evt["payload"])
		require.NotContains(t, evt, "level")
		require.NotContains(t, evt, "msg")
	}
}
