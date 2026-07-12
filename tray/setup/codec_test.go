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
			// Plan -> Engine Config (filter knobs no longer written
			// here; Filter rides on the tray-side config now).
			engine := tc.plan.ToEngineConfig(*config.GetConfig())
			tray := TrayConfig{
				AutoStart:   tc.plan.App.AutoStart,
				NotifyPrefs: tc.plan.Notify,
				Filter:      tc.plan.Filter,
			}

			// Engine Config + Tray -> Plan
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
			// Engine config must NOT carry the per-target filter
			// knobs; tray config is the matching authority.
			if cardano, ok := engine.Plugin["filter"]["cardano"]; ok {
				assert.NotContains(t, cardano, "address",
					"engine filter must not carry address knob")
				assert.NotContains(t, cardano, "drep",
					"engine filter must not carry drep knob")
				assert.NotContains(t, cardano, "pool",
					"engine filter must not carry pool knob")
			}
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

// TestToEngineConfigClearsCardanoFilterKnobs is the regression guard
// for keeping UI targets out of the sidecar's cardano-filter block.
// The tray notification engine owns target matching semantics, so
// ToEngineConfig must defensively scrub any stale knobs left over from
// earlier tray versions and must NOT write the plan's Filter into the
// engine config.
func TestToEngineConfigClearsCardanoFilterKnobs(t *testing.T) {
	base := config.Config{
		Plugin: map[string]map[string]map[any]any{
			"filter": {
				"cardano": {
					"address": "addr1old",
					"drep":    "drep1old",
					"pool":    "pool1old",
					"asset":   "asset1old",
					"policy":  "polold",
				},
			},
		},
	}
	plan := SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter: FilterConfig{
			Wallets:  []string{"addr1new"},
			DReps:    []string{"drep1new"},
			Pools:    []string{"pool1new"},
			Assets:   []string{"asset1new"},
			Policies: []string{"polnew"},
		},
		Output: OutputConfig{
			Type:   "none",
			Config: make(map[string]string),
		},
	}

	got := plan.ToEngineConfig(base)
	cardano := got.Plugin["filter"]["cardano"]

	// All five legacy knobs must be gone — regardless of whether
	// the plan has matching targets. Writing any of them back would
	// split target semantics between the tray and sidecar configs.
	assert.NotContains(t, cardano, "address",
		"address knob must not be written by ToEngineConfig")
	assert.NotContains(t, cardano, "drep",
		"drep knob must not be written by ToEngineConfig")
	assert.NotContains(t, cardano, "pool",
		"pool knob must not be written by ToEngineConfig")
	assert.NotContains(t, cardano, "asset",
		"asset knob must not be written by ToEngineConfig")
	assert.NotContains(t, cardano, "policy",
		"policy knob must not be written by ToEngineConfig")
}

// TestToEngineConfig_CombinedWalletDRepPoolNeverWritesFilterKnobs is
// the regression guard for keeping a multi-section filter in one
// authority: a user who configures wallet + DRep + pool together must
// NOT have those values written into the sidecar's cardano filter.
// ToEngineConfig leaves the filter knobs unset regardless of plan
// content; target matching happens at the tray engine layer.
func TestToEngineConfig_CombinedWalletDRepPoolNeverWritesFilterKnobs(
	t *testing.T,
) {
	plan := SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter: FilterConfig{
			Wallets: []string{"addr1aa", "addr1bb"},
			DReps:   []string{"drep1cc"},
			Pools:   []string{"pool1dd", "pool1ee"},
		},
		Output: OutputConfig{
			Type:   "none",
			Config: map[string]string{},
		},
	}
	got := plan.ToEngineConfig(config.Config{})

	if cardano, ok := got.Plugin["filter"]["cardano"]; ok {
		assert.NotContains(t, cardano, "address",
			"combined plan must not write address knob "+
				"(would AND-restrict tx events)")
		assert.NotContains(t, cardano, "drep",
			"combined plan must not write drep knob")
		assert.NotContains(t, cardano, "pool",
			"combined plan must not write pool knob")
	}
}

// TestSetupPlanFromEngineConfigMigratesLegacyCardanoKnobs covers the
// upgrade path: a user whose engine.yaml still has filter.cardano
// entries from the pre-OR tray version sees those targets migrated
// into plan.Filter (and on the next SaveTrayAtomic + ToEngineConfig
// cycle, the legacy knobs get scrubbed from engine.yaml).
func TestSetupPlanFromEngineConfigMigratesLegacyCardanoKnobs(t *testing.T) {
	engine := config.Config{
		Plugin: map[string]map[string]map[any]any{
			"filter": {
				"cardano": {
					"address": "addr1legacy",
					"drep":    "drep1legacy",
				},
			},
		},
	}
	// Tray has no Filter set yet (fresh on this version).
	got := SetupPlanFromEngineConfig(engine, TrayConfig{})

	assert.False(t, got.Filter.MonitorEverything,
		"legacy non-empty knobs must NOT collapse to MonitorEverything")
	assert.Equal(t,
		[]string{"addr1legacy"}, got.Filter.Wallets,
		"legacy address knob must be migrated into Wallets")
	assert.Equal(t,
		[]string{"drep1legacy"}, got.Filter.DReps,
		"legacy drep knob must be migrated into DReps")
	assert.Empty(t, got.Filter.Pools,
		"absent legacy pool knob must yield empty Pools")
}

// TestSetupPlanFromEngineConfigMigratesLegacyAssetAndPolicyKnobs is the
// regression guard for the upgrade-path data-loss bug: the cardano
// filter plugin accepts asset and policy knobs too, and a hand-edited
// engine config (or one carried over from CLI usage) may have them
// set. Without the migration, those values were silently scrubbed on
// the first reconfigure by ToEngineConfig and never reached
// plan.Filter.
func TestSetupPlanFromEngineConfigMigratesLegacyAssetAndPolicyKnobs(
	t *testing.T,
) {
	engine := config.Config{
		Plugin: map[string]map[string]map[any]any{
			"filter": {
				"cardano": {
					"asset":  "asset1abc,asset1def",
					"policy": "pol1,pol2",
				},
			},
		},
	}
	got := SetupPlanFromEngineConfig(engine, TrayConfig{})

	assert.False(t, got.Filter.MonitorEverything,
		"legacy non-empty asset/policy knobs must NOT collapse "+
			"to MonitorEverything")
	assert.Equal(t,
		[]string{"asset1abc", "asset1def"}, got.Filter.Assets,
		"legacy asset knob must be migrated into Assets (CSV split)")
	assert.Equal(t,
		[]string{"pol1", "pol2"}, got.Filter.Policies,
		"legacy policy knob must be migrated into Policies (CSV split)")
}

// TestSetupPlanFromEngineConfigDoesNotAliasFilterSlices is the
// regression guard for the FilterConfig slice-aliasing finding: the
// codec must deep-copy the five slice fields, not just the struct
// header. Without CloneFilter, a later append-grow on either side
// would silently mutate the other — same hazard as NotifyPrefs map
// aliasing, but the slice fields were missed.
func TestSetupPlanFromEngineConfigDoesNotAliasFilterSlices(t *testing.T) {
	tray := TrayConfig{
		Filter: FilterConfig{
			Wallets:  []string{"addr1a"},
			DReps:    []string{"drep1a"},
			Pools:    []string{"pool1a"},
			Assets:   []string{"asset1a"},
			Policies: []string{"pol1"},
		},
	}
	got := SetupPlanFromEngineConfig(config.Config{}, tray)

	// Mutate the source slices post-call; the produced plan must NOT
	// see the changes.
	tray.Filter.Wallets[0] = "MUTATED"
	tray.Filter.DReps[0] = "MUTATED"
	tray.Filter.Pools[0] = "MUTATED"
	tray.Filter.Assets[0] = "MUTATED"
	tray.Filter.Policies[0] = "MUTATED"

	assert.Equal(t, "addr1a", got.Filter.Wallets[0],
		"Wallets backing array must be detached from tray.Filter")
	assert.Equal(t, "drep1a", got.Filter.DReps[0],
		"DReps backing array must be detached")
	assert.Equal(t, "pool1a", got.Filter.Pools[0],
		"Pools backing array must be detached")
	assert.Equal(t, "asset1a", got.Filter.Assets[0],
		"Assets backing array must be detached")
	assert.Equal(t, "pol1", got.Filter.Policies[0],
		"Policies backing array must be detached")
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
