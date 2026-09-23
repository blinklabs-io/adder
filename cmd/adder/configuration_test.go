// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestRegisteredFactories(t *testing.T) {
	credentials := filepath.Join(t.TempDir(), "service.json")
	require.NoError(
		t,
		os.WriteFile(
			credentials,
			[]byte(`{"project_id":"test-project"}`),
			0o600,
		),
	)
	notification := filepath.Join(t.TempDir(), "notifications.json")
	notificationConfig := setup.DefaultNotificationConfig()
	notificationConfig.Monitor.Everything = true
	data, err := json.Marshal(notificationConfig)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(notification, data, 0o600))
	overrides := map[string]map[string]any{
		"mempool":     {"socket-path": "/tmp/not-opened.sock"},
		"utxorpc":     {"url": "https://example.invalid"},
		"push":        {"serviceAccountFilePath": credentials},
		"telegram":    {"bot-token": "123:test-secret", "chat-id": "-123"},
		"notify-json": {"config": notification},
	}
	for _, kind := range []plugin.PluginType{plugin.PluginTypeInput, plugin.PluginTypeFilter, plugin.PluginTypeOutput} {
		for _, entry := range plugin.GetPlugins(kind) {
			t.Run(
				plugin.PluginTypeName(kind)+"/"+entry.Name,
				func(t *testing.T) {
					p, err := entry.New(overrides[entry.Name])
					require.NoError(t, err)
					require.NotNil(t, p)
					require.Equal(t, kind, p.Role())
					require.Nil(t, p.InputChan())
					require.Nil(t, p.OutputChan())
					other, err := entry.New(overrides[entry.Name])
					require.NoError(t, err)
					require.NotSame(t, p, other)
				},
			)
		}
	}
}

func TestFactoryErrorsAreDescriptive(t *testing.T) {
	for _, tt := range []struct {
		kind    plugin.PluginType
		name    string
		values  map[string]any
		message string
	}{
		{plugin.PluginTypeInput, "chainsync", map[string]any{"network": "typo"}, "network"},
		{plugin.PluginTypeInput, "chainsync", map[string]any{"network-magic": uint64(1) << 32}, "network-magic"},
		{plugin.PluginTypeInput, "chainsync", map[string]any{"intersect-point": "1.ab"}, "hash"},
		{plugin.PluginTypeInput, "mempool", map[string]any{"socket-path": "/tmp/not-opened.sock", "poll-interval": "0s"}, "poll-interval"},
		{plugin.PluginTypeInput, "utxorpc", map[string]any{"url": "https://example.invalid", "mode": "typo"}, "mode"},
		{plugin.PluginTypeInput, "utxorpc", map[string]any{"url": "https://example.invalid", "intersect-point": "typo"}, "intersect-point"},
		{plugin.PluginTypeOutput, "log", map[string]any{"format": "typo"}, "format"},
		{plugin.PluginTypeOutput, "webhook", map[string]any{"url": "https://secret:password@%"}, "url"},
		{plugin.PluginTypeOutput, "telegram", map[string]any{"chat-id": "typo"}, "chat-id"},
		{plugin.PluginTypeOutput, "push", nil, "service account"},
		{plugin.PluginTypeOutput, "notify-json", nil, "config"},
	} {
		t.Run(tt.name+"/"+tt.message, func(t *testing.T) {
			p, err := plugin.GetPlugin(tt.kind, tt.name, tt.values)
			require.ErrorContains(t, err, tt.message)
			require.Nil(t, p)
			require.NotContains(t, err.Error(), "password")
		})
	}
}

func TestCLIPluginPrecedenceAndKupoEmpty(t *testing.T) {
	t.Setenv("FILTER_EVENT_TYPE", "input.rollback")
	t.Setenv("KUPO_URL", "https://example.invalid")
	c := config.New()
	c.Plugin = map[string]map[string]map[string]any{
		"filter": {"event": {"type": "input.transaction"}},
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, c.BindFlags(fs))
	require.NoError(
		t,
		fs.Parse(
			[]string{"--filter-type=input.block", "--input-mempool-kupo-url="},
		),
	)
	resolved, err := c.ResolvePlugins(fs)
	require.NoError(t, err)
	p, err := resolved.New(plugin.PluginTypeFilter, "event")
	require.NoError(t, err)
	require.NotNil(t, p)
	// An invalid ambient endpoint would fail factory validation if explicit empty lost.
	t.Setenv("KUPO_URL", "invalid-url")
	require.NoError(
		t,
		fs.Set("input-mempool-socket-path", "/tmp/not-opened.sock"),
	)
	resolved, err = c.ResolvePlugins(fs)
	require.NoError(t, err)
	p, err = resolved.New(plugin.PluginTypeInput, "mempool")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Nil(t, c.Plugin["input"], "resolution must not mutate caller maps")
}

func TestSingleLoggingLevelSetting(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	c := config.New()
	require.NoError(t, c.BindFlags(fs))
	require.NotNil(t, fs.Lookup("output-log-level"))
	require.Nil(t, fs.Lookup("logging-level"))
	require.ErrorContains(t, fs.Parse([]string{"--logging-level=debug"}), "unknown flag")
	path := filepath.Join(t.TempDir(), "old.yaml")
	require.NoError(t, os.WriteFile(path, []byte("logging:\n  level: debug\n"), 0600))
	require.ErrorContains(t, c.Load(path), "field logging not found")
}
