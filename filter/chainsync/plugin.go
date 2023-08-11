// Copyright 2023 Blink Labs, LLC.
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

package chainsync

import (
	"strings"

	"github.com/blinklabs-io/snek/plugin"
)

var cmdlineOptions struct {
	address  string
	policyId string
	asset    string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeFilter,
			Name:               "chainsync",
			Description:        "filters chainsync events",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies address to filter on",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.address),
					CustomFlag:   "address",
				},
				{
					Name:         "policy",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies asset policy ID to filter on",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.policyId),
					CustomFlag:   "policy",
				},
				{
					Name:         "asset",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the asset fingerprint (asset1xxx) to filter on",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.asset),
					CustomFlag:   "asset",
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	pluginOptions := []ChainSyncOptionFunc{}
	if cmdlineOptions.address != "" {
		pluginOptions = append(
			pluginOptions,
			WithAddresses(
				strings.Split(cmdlineOptions.address, ","),
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
	if cmdlineOptions.asset != "" {
		pluginOptions = append(
			pluginOptions,
			WithAssetFingerprints(
				strings.Split(cmdlineOptions.asset, ","),
			),
		)
	}
	p := New(pluginOptions...)
	return p
}
