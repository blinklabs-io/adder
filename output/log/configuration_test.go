// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package log

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestLevelControlsOnlyItsOutput(t *testing.T) {
	for _, format := range []string{FormatText, FormatJSON} {
		for _, level := range []string{"debug", "info", "warn", "error"} {
			t.Run(format+"/"+level, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "events.log")
				p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "log", map[string]any{
					"format": format, "level": level, "path": path,
				})
				require.NoError(t, err)
				output, ok := p.(*LogOutput)
				require.True(t, ok)
				require.NotNil(t, output)
				require.NoError(t, output.Start())
				t.Cleanup(func() { require.NoError(t, output.Stop()) })
				otherPath := filepath.Join(t.TempDir(), "other.log")
				other := New(WithFormat(format), WithFilePath(otherPath))
				require.NoError(t, other.Start())
				t.Cleanup(func() { require.NoError(t, other.Stop()) })
				events := []event.Event{
					{Type: event.TypeBlock, Payload: event.BlockEvent{}},
					{Type: event.TypeTransaction, Payload: event.TransactionEvent{}},
					{Type: event.TypeRollback, Payload: event.RollbackEvent{}},
					{Type: event.TypeGovernance, Payload: event.GovernanceEvent{}},
					{Type: event.TypeDRepRegistration, Payload: map[string]any{"certificate": "registration"}},
					{Type: event.TypeDRepUpdate, Payload: map[string]any{"certificate": "update"}},
					{Type: event.TypeDRepRetirement, Payload: map[string]any{"certificate": "retirement"}},
				}
				for range 20 {
					for _, evt := range events {
						for _, sink := range []*LogOutput{output, other} {
							select {
							case sink.InputChan() <- evt:
							case <-time.After(time.Second):
								t.Fatal("output stopped consuming events")
							}
						}
					}
				}
				require.NoError(t, output.Stop())
				require.NoError(t, other.Stop())
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				otherData, err := os.ReadFile(otherPath)
				require.NoError(t, err)
				require.Equal(t, 20*len(events), strings.Count(string(otherData), "\n"))
				if level == "warn" || level == "error" {
					require.Empty(t, data)
				} else {
					require.Equal(t, otherData, data)
				}
			})
		}
	}
}

func TestLevelConfigurationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml map[string]map[string]map[string]any
		env  map[string]string
		args []string
		want slog.Level
	}{
		{name: "default", want: slog.LevelInfo},
		{name: "yaml", yaml: map[string]map[string]map[string]any{"output": {"log": {"level": "warn"}}}, want: slog.LevelWarn},
		{name: "environment", yaml: map[string]map[string]map[string]any{"output": {"log": {"level": "warn"}}}, env: map[string]string{"OUTPUT_LOG_LEVEL": "error"}, want: slog.LevelError},
		{name: "cli", yaml: map[string]map[string]map[string]any{"output": {"log": {"level": "warn"}}}, env: map[string]string{"OUTPUT_LOG_LEVEL": "error"}, args: []string{"--output-log-level=debug"}, want: slog.LevelDebug},
		{name: "explicit-default", env: map[string]string{"OUTPUT_LOG_LEVEL": "error"}, args: []string{"--output-log-level=info"}, want: slog.LevelInfo},
		{name: "removed-environment-setting-is-ignored", env: map[string]string{"LOGGING_LEVEL": "error"}, want: slog.LevelInfo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			require.NoError(t, plugin.PopulateCmdlineOptions(fs))
			require.NoError(t, fs.Parse(tc.args))
			resolved, err := plugin.ResolveConfig(tc.yaml, fs, func(key string) (string, bool) {
				value, ok := tc.env[key]
				return value, ok
			})
			require.NoError(t, err)
			p, err := resolved.New(plugin.PluginTypeOutput, "log")
			require.NoError(t, err)
			output, ok := p.(*LogOutput)
			require.True(t, ok)
			require.NotNil(t, output)
			require.Equal(t, tc.want, output.level)
		})
	}
}

func TestRejectInvalidLevel(t *testing.T) {
	for _, level := range []string{"", "trace", "infp", "none", "off"} {
		t.Run(level, func(t *testing.T) {
			p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "log", map[string]any{"level": level})
			require.ErrorContains(t, err, "level must be debug, info, warn, or error")
			require.Nil(t, p)
		})
	}
}
