// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package mempool

import (
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func mustConfiguredPlugin(
	t *testing.T,
	values map[string]any,
) plugin.ManagedPlugin {
	t.Helper()
	p, err := plugin.GetPlugin(plugin.PluginTypeInput, "mempool", values)
	require.NoError(t, err)
	return p
}
