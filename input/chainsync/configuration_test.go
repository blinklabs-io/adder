// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package chainsync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/state"
	"github.com/stretchr/testify/require"
)

func mustConfiguredPlugin(
	t *testing.T,
	values map[string]any,
) plugin.ManagedPlugin {
	t.Helper()
	p, err := plugin.GetPlugin(plugin.PluginTypeInput, "chainsync", values)
	require.NoError(t, err)
	return p
}

func TestNetworkMagicOverrideSurvivesNetworkLookup(t *testing.T) {
	p := mustConfiguredPlugin(
		t,
		map[string]any{
			"network":       "mainnet",
			"network-magic": uint64(4294967295),
		},
	)
	c := p.(*ChainSync)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, c.setupConnection(ctx))
	require.Equal(t, uint32(4294967295), c.networkMagic)
}

func TestStatePathConfiguresSqliteStore(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "cursor.sqlite")
	p := mustConfiguredPlugin(
		t,
		map[string]any{
			"network":    "mainnet",
			"state-path": statePath,
		},
	)
	c := p.(*ChainSync)
	require.NotNil(t, c.stateStore)
	_, ok := c.stateStore.(*state.SqliteStore)
	require.True(t, ok)
	require.NoError(t, c.stateStore.Close())
}

func TestStatePathUnsetLeavesStoreNil(t *testing.T) {
	p := mustConfiguredPlugin(
		t,
		map[string]any{
			"network": "mainnet",
		},
	)
	c := p.(*ChainSync)
	require.Nil(t, c.stateStore)
}
