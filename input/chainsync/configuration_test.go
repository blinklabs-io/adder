// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package chainsync

import (
	"context"
	"testing"

	"github.com/blinklabs-io/adder/plugin"
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
