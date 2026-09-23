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

package cardano

import (
	"strings"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeFilter,
			Name:               "cardano",
			Description:        "filters Cardano blockchain events by address, asset, policy, pool, or DRep",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies address(es) to filter on (comma-separated)",
					DefaultValue: "",
					CustomFlag:   "address",
				},
				{
					Name:         "asset",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies asset fingerprint(s) to filter on (comma-separated)",
					DefaultValue: "",
					CustomFlag:   "asset",
				},
				{
					Name:         "policy",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies asset policy ID(s) to filter on (comma-separated)",
					DefaultValue: "",
					CustomFlag:   "policy",
				},
				{
					Name:         "pool",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies Pool ID(s) to filter on (comma-separated)",
					DefaultValue: "",
					CustomFlag:   "pool",
				},
				{
					Name:         "drep",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies DRep ID(s) to filter on (comma-separated, hex or bech32)",
					DefaultValue: "",
					CustomFlag:   "drep",
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	pluginOptions := []CardanoOptionFunc{
		WithLogger(
			logging.GetLogger().With("plugin", "filter.cardano"),
		),
	}
	if addresses := plugin.SplitAndTrim(values.String("address")); len(
		addresses,
	) > 0 {
		pluginOptions = append(
			pluginOptions,
			WithAddresses(addresses),
		)
	}
	if assets := plugin.SplitAndTrim(values.String("asset")); len(assets) > 0 {
		pluginOptions = append(
			pluginOptions,
			WithAssetFingerprints(assets),
		)
	}
	if policyIds := plugin.SplitAndTrim(values.String("policy")); len(
		policyIds,
	) > 0 {
		pluginOptions = append(
			pluginOptions,
			WithPolicies(policyIds),
		)
	}
	if poolIds := plugin.SplitAndTrim(values.String("pool")); len(poolIds) > 0 {
		pluginOptions = append(
			pluginOptions,
			WithPoolIds(poolIds),
		)
	}
	if drepIds := plugin.SplitAndTrim(
		strings.ToLower(values.String("drep")),
	); len(drepIds) > 0 {
		pluginOptions = append(
			pluginOptions,
			WithDRepIds(drepIds),
		)
	}
	p := New(pluginOptions...)
	return p, nil
}
