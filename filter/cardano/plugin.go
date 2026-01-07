// Copyright 2025 Blink Labs Software
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

var cmdlineOptions struct {
	address  string
	asset    string
	policyId string
	poolId   string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeFilter,
			Name:               "cardano",
			Description:        "filters Cardano blockchain events by address, asset, policy, or pool",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies address(es) to filter on (comma-separated)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.address),
					CustomFlag:   "address",
				},
				{
					Name:         "asset",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies asset fingerprint(s) to filter on (comma-separated)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.asset),
					CustomFlag:   "asset",
				},
				{
					Name:         "policy",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies asset policy ID(s) to filter on (comma-separated)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.policyId),
					CustomFlag:   "policy",
				},
				{
					Name:         "pool",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies Pool ID(s) to filter on (comma-separated)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.poolId),
					CustomFlag:   "pool",
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	pluginOptions := []CardanoOptionFunc{
		WithLogger(
			logging.GetLogger().With("plugin", "filter.cardano"),
		),
	}
	if cmdlineOptions.address != "" {
		pluginOptions = append(
			pluginOptions,
			WithAddresses(
				strings.Split(cmdlineOptions.address, ","),
			),
		)
	}
	if cmdlineOptions.asset != "" {
		pluginOptions = append(
			pluginOptions,
			WithAssetFingerprints(
				strings.Split(cmdlineOptions.asset, ","),
			),
		)
	}
	if cmdlineOptions.policyId != "" {
		pluginOptions = append(
			pluginOptions,
			WithPolicies(
				strings.Split(cmdlineOptions.policyId, ","),
			),
		)
	}
	if cmdlineOptions.poolId != "" {
		pluginOptions = append(
			pluginOptions,
			WithPoolIds(
				strings.Split(cmdlineOptions.poolId, ","),
			),
		)
	}
	p := New(pluginOptions...)
	return p
}
