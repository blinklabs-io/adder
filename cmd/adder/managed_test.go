// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestManagedRejectsGlobalFiltersAndDurableOrHistoricalSource(t *testing.T) {
	previous := cfg
	cfg = config.New()
	t.Cleanup(func() { cfg = previous })
	for _, tc := range []struct {
		name, kind, plugin, option string
		value                      any
	}{
		{"event filter", "filter", "event", "type", "input.block"},
		{"cardano filter", "filter", "cardano", "pool", "sentinel"},
		{"durable cursor", "input", "chainsync", "state-path", "cursor.sqlite"},
		{"from genesis", "input", "chainsync", "intersect-tip", false},
		{"no reconnect", "input", "chainsync", "auto-reconnect", false},
		{"explicit cursor", "input", "chainsync", "intersect-point", "123.hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := plugin.ResolveConfig(map[string]map[string]map[string]any{tc.kind: {tc.plugin: {tc.option: tc.value}}}, nil, func(string) (string, bool) { return "", false })
			require.NoError(t, err)
			require.Error(t, validateManagedInput(resolved))
		})
	}
	resolved, err := plugin.ResolveConfig(nil, nil, func(string) (string, bool) { return "", false })
	require.NoError(t, err)
	require.NoError(t, validateManagedInput(resolved))
	cfg.Input = "utxorpc"
	require.Error(t, validateManagedInput(resolved))
}

func TestManagedTokenRequiresRestrictedRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "operator.token")
	require.NoError(t, os.WriteFile(path, []byte("0123456789abcdef0123456789abcdef\n"), 0o600))
	token, err := managedToken(path)
	require.NoError(t, err)
	require.Equal(t, "0123456789abcdef0123456789abcdef", token)
	require.NoError(t, os.Chmod(path, 0o644))
	_, err = managedToken(path)
	require.Error(t, err)
	_, err = managedToken(dir)
	require.Error(t, err)
}
