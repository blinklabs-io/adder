// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package setup

import (
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestSetupPlanRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		plan SetupPlan
	}{
		{
			name: "Watch Wallet on Preprod",
			plan: SetupPlan{
				Network: NetworkConfig{Name: "preprod"},
				Filter: FilterConfig{
					Wallets: []string{
						"addr1qxy648m6k96350t4tql82q0e8sqpks54uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g58xvsf2y066",
					},
				},
				Output: OutputConfig{
					Type:   "none",
					Config: map[string]string{},
				},
				API:    APIConfig{Address: "127.0.0.1", Port: 8080},
				Notify: NotificationPrefs{},
				App:    AppConfig{AutoStart: true},
			},
		},
		{
			name: "Track DRep with Webhook",
			plan: SetupPlan{
				Network: NetworkConfig{Name: "mainnet"},
				Filter: FilterConfig{
					DReps: []string{
						"drep1qxy648m6k96350t4tql82q0e8sqpks54uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g58xvsf2y066",
					},
				},
				Output: OutputConfig{
					Type: "webhook",
					Config: map[string]string{
						"url":    "http://example.com/webhook",
						"format": "adder",
					},
				},
				API:    APIConfig{Address: "0.0.0.0", Port: 9090},
				Notify: NotificationPrefs{"Votes cast": true},
				App:    AppConfig{AutoStart: false},
			},
		},
		{
			name: "Monitor Everything with Log to File",
			plan: SetupPlan{
				Network: NetworkConfig{Name: "preview"},
				Filter:  FilterConfig{MonitorEverything: true},
				Output: OutputConfig{
					Type: "log",
					Config: map[string]string{
						"path": "/tmp/adder.log",
					},
				},
				API:    APIConfig{Address: "localhost", Port: 8081},
				Notify: NotificationPrefs{"Blocks minted": true},
				App:    AppConfig{AutoStart: true},
			},
		},
		{
			name: "Monitor Pool with Telegram",
			plan: SetupPlan{
				Network: NetworkConfig{Name: "mainnet"},
				Filter: FilterConfig{
					Pools: []string{
						"pool1qxy648m6k96350t4tql82q0e8sqpks54uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g58xvsf2y066",
					},
				},
				Output: OutputConfig{
					Type: "telegram",
					Config: map[string]string{
						"token":   "123456:ABC",
						"chat_id": "123456789",
					},
				},
				API:    APIConfig{Address: "127.0.0.1", Port: 8082},
				Notify: NotificationPrefs{"Pool parameter changes": true},
				App:    AppConfig{AutoStart: false},
			},
		},
		{
			// New: combined wallet+DRep+pool exercises the multi-target
			// round-trip — all three knobs survive ToEngineConfig and
			// SetupPlanFromEngineConfig reassembles the same lists.
			name: "Combined wallet + DRep + pool",
			plan: SetupPlan{
				Network: NetworkConfig{Name: "mainnet"},
				Filter: FilterConfig{
					Wallets: []string{"addr1xyz", "stake1xyz"},
					DReps:   []string{"drep1abc"},
					Pools:   []string{"pool1abc", "pool1def"},
				},
				Output: OutputConfig{
					Type:   "none",
					Config: map[string]string{},
				},
				API:    APIConfig{Address: "127.0.0.1", Port: 8080},
				Notify: NotificationPrefs{},
				App:    AppConfig{AutoStart: false},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Plan -> Engine Config
			engine := tc.plan.ToEngineConfig(*config.GetConfig())
			tray := TrayConfig{
				AutoStart:   tc.plan.App.AutoStart,
				NotifyPrefs: tc.plan.Notify,
			}

			// Engine Config -> Plan
			got := SetupPlanFromEngineConfig(engine, tray)

			// Assert round-trip equality
			assert.Equal(t, tc.plan.Network.Name, got.Network.Name)
			assert.Equal(t,
				tc.plan.Filter.MonitorEverything,
				got.Filter.MonitorEverything)
			assert.Equal(t, tc.plan.Filter.Wallets, got.Filter.Wallets)
			assert.Equal(t, tc.plan.Filter.DReps, got.Filter.DReps)
			assert.Equal(t, tc.plan.Filter.Pools, got.Filter.Pools)
			assert.Equal(t, tc.plan.Output.Type, got.Output.Type)
			assert.Equal(t, tc.plan.Output.Config, got.Output.Config)
			assert.Equal(t, tc.plan.API.Address, got.API.Address)
			assert.Equal(t, tc.plan.API.Port, got.API.Port)
			assert.Equal(t, tc.plan.App.AutoStart, got.App.AutoStart)
			assert.Equal(t, tc.plan.Notify, got.Notify)
		})
	}
}

func TestSetupPlanFromEngineConfigHandlesCustomAddressAndLogDefaults(
	t *testing.T,
) {
	engine := config.Config{
		Api: config.ApiConfig{
			ListenAddress: "0.0.0.0",
			ListenPort:    9090,
		},
		Output: "log",
		Plugin: map[string]map[string]map[any]any{
			"input": {
				"chainsync": {
					"network": "preprod",
					"address": "node.example.test:3001",
				},
			},
			"filter": {
				"cardano": {},
			},
			"output": {
				"log": {
					"format": "text",
				},
			},
		},
	}

	got := SetupPlanFromEngineConfig(engine, TrayConfig{
		AutoStart: true,
		NotifyPrefs: map[string]bool{
			NotifyPrefConnectionIssues: true,
		},
	})

	assert.Equal(t, "preprod", got.Network.Name)
	assert.Equal(t, "node.example.test", got.Network.CustomAddress)
	assert.Equal(t, uint(3001), got.Network.CustomPort)
	// Empty cardano-filter section → MonitorEverything.
	assert.True(t, got.Filter.MonitorEverything)
	assert.Empty(t, got.Filter.Wallets)
	assert.Empty(t, got.Filter.DReps)
	assert.Empty(t, got.Filter.Pools)
	assert.Equal(t, "none", got.Output.Type)
	assert.True(t, got.App.AutoStart)
	assert.True(t, got.Notify[NotifyPrefConnectionIssues])
}

// TestSetupPlanFromEngineConfigDoesNotAliasNotifyPrefs guards the
// review-feedback regression: NotificationPrefs is map[string]bool, so
// a type conversion shares the underlying map header. The reconstructed
// plan must hold a defensive copy or mutating plan.Notify silently
// mutates the caller's TrayConfig.
func TestSetupPlanFromEngineConfigDoesNotAliasNotifyPrefs(t *testing.T) {
	tray := TrayConfig{
		NotifyPrefs: map[string]bool{
			NotifyPrefBlocksMinted: true,
		},
	}
	plan := SetupPlanFromEngineConfig(config.Config{}, tray)

	// Mutate the plan's prefs; the caller's tray map must not change.
	plan.Notify[NotifyPrefBlocksMinted] = false
	plan.Notify["new-key"] = true

	assert.True(t, tray.NotifyPrefs[NotifyPrefBlocksMinted],
		"mutating plan.Notify must not write back to tray.NotifyPrefs")
	_, hasNewKey := tray.NotifyPrefs["new-key"]
	assert.False(t, hasNewKey,
		"adding to plan.Notify must not appear in tray.NotifyPrefs")
}

func TestSetupPlanFromEngineConfigHandlesSparseConfig(t *testing.T) {
	got := SetupPlanFromEngineConfig(config.Config{}, TrayConfig{})

	assert.Empty(t, got.Network.Name)
	// No cardano-filter section AND no target lists ⇒
	// MonitorEverything. Treating "no filter knobs at all" identically
	// to "filter section present but empty" keeps the wizard
	// reconfigure path symmetric: a hand-edited config that drops the
	// filter subtree round-trips through the wizard instead of
	// wedging step 3.
	assert.True(t, got.Filter.MonitorEverything)
	assert.Empty(t, got.Filter.Wallets)
	assert.Empty(t, got.Filter.DReps)
	assert.Empty(t, got.Filter.Pools)
	assert.Equal(t, "none", got.Output.Type)
	assert.NotNil(t, got.Output.Config)
}

func TestToEngineConfigClearsPreviousCardanoFilters(t *testing.T) {
	base := config.Config{
		Plugin: map[string]map[string]map[any]any{
			"filter": {
				"cardano": {
					"address": "addr1old",
					"drep":    "drep1old",
					"pool":    "pool1old",
				},
			},
		},
	}
	plan := SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter: FilterConfig{
			Pools: []string{"pool1new"},
		},
		Output: OutputConfig{
			Type:   "none",
			Config: make(map[string]string),
		},
	}

	got := plan.ToEngineConfig(base)
	cardano := got.Plugin["filter"]["cardano"]

	assert.NotContains(t, cardano, "address")
	assert.NotContains(t, cardano, "drep")
	assert.Equal(t, "pool1new", cardano["pool"])
}

func TestToEngineConfigWritesCustomNodeAddress(t *testing.T) {
	plan := SetupPlan{
		Network: NetworkConfig{
			Name:          "preview",
			CustomAddress: "node.example.test",
			CustomPort:    3001,
		},
		Filter: FilterConfig{MonitorEverything: true},
		Output: OutputConfig{
			Type:   "none",
			Config: make(map[string]string),
		},
	}

	got := plan.ToEngineConfig(config.Config{})

	assert.Equal(t, "preview", got.Plugin["input"]["chainsync"]["network"])
	assert.Equal(
		t,
		"node.example.test:3001",
		got.Plugin["input"]["chainsync"]["address"],
	)
}
