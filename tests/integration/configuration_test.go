//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigurationIntegration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config settings
		flags  []string
		path   string
	}{
		{name: "environment-over-yaml", config: settings{
			yaml: "output: webhook\nplugins:\n  output:\n    log:\n      format: text\n      level: warn\n      path: yaml.jsonl\n",
			env:  []string{"OUTPUT=log", "OUTPUT_LOG_FORMAT=json", "OUTPUT_LOG_LEVEL=info", "OUTPUT_LOG_PATH=env.jsonl"},
		}, path: "env.jsonl"},
		{name: "cli-over-environment-and-yaml", config: settings{
			yaml: "output: webhook\nplugins:\n  output:\n    log:\n      format: text\n      level: warn\n      path: yaml.jsonl\n",
			env:  []string{"OUTPUT=webhook", "OUTPUT_LOG_FORMAT=text", "OUTPUT_LOG_LEVEL=error", "OUTPUT_LOG_PATH=env.jsonl"},
		}, flags: []string{"--output", "log", "--output-log-format", "json", "--output-log-level", "debug", "--output-log-path", "cli.jsonl"},
			path: "cli.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fixture{release: make(chan struct{})}
			p := launchAdder(t, f, tc.config, tc.flags...)
			p.healthy(t)
			close(f.release)
			want := []string{"input.block", "input.transaction", "input.governance", "input.rollback"}
			require.Equal(t, want, p.events(t, len(want)))
			p.waitOutput(t, tc.path, len(want))
			p.stop(t, syscall.SIGTERM)
			checkJSON(t, p.read(t, tc.path), want)
			require.Empty(t, p.read(t, "stdout.log"))
			require.Contains(t, p.read(t, "stderr.log"), "started utxorpc input")
			for _, other := range []string{"yaml.jsonl", "env.jsonl", "cli.jsonl"} {
				if other != tc.path {
					_, err := os.Stat(filepath.Join(p.dir, other))
					require.True(t, os.IsNotExist(err), "unexpected output file %s", other)
				}
			}
		})
	}
}
