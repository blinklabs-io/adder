// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package telegram

import (
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestFactoryConstructionResult(t *testing.T) {
	for _, token := range []string{"", "123:test"} {
		t.Run(token, func(t *testing.T) {
			cfg, err := plugin.ResolveConfig(
				map[string]map[string]map[string]any{
					"output": {
						"telegram": {"chat-id": "123", "bot-token": token},
					},
				}, nil, func(string) (string, bool) { return "", false },
			)
			require.NoError(t, err)
			options, err := cfg.Options(plugin.PluginTypeOutput, "telegram")
			require.NoError(t, err)
			p, err := newFromOptions(options)
			if token == "" {
				require.ErrorContains(t, err, "telegram bot token is required")
				require.True(
					t,
					p == nil,
					"failed factory returned a non-nil interface",
				)
			} else {
				require.NoError(t, err)
				require.IsType(t, &TelegramOutput{}, p)
			}
		})
	}
}

func TestFactoryAcceptsEmptyOperatorEntries(t *testing.T) {
	cfg, err := plugin.ResolveConfig(
		map[string]map[string]map[string]any{
			"output": {"telegram": {
				"chat-id": "99", "bot-token": "123:test",
				"operator-user-ids": "42,,  ,43,",
			}},
		}, nil, func(string) (string, bool) { return "", false },
	)
	require.NoError(t, err)
	options, err := cfg.Options(plugin.PluginTypeOutput, "telegram")
	require.NoError(t, err)
	p, err := newFromOptions(options)
	require.NoError(t, err)
	tg := p.(*TelegramOutput)
	require.Contains(t, tg.operatorIDs, int64(42))
	require.Contains(t, tg.operatorIDs, int64(43))
}
