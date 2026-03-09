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

package utxorpc

import (
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var cmdlineOptions struct {
	url            string
	mode           string
	network        string
	apiKeyHeader   string
	apiKey         string
	intersectTip   bool
	intersectPoint string
	autoReconnect  bool
	includeCbor    bool
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:        plugin.PluginTypeInput,
			Name:        "utxorpc",
			Description: "reads blocks and transactions from a UTxO RPC provider over gRPC streaming endpoints",
			NewFromOptionsFunc: func() plugin.Plugin {
				return NewFromCmdlineOptions()
			},
			Options: []plugin.PluginOption{
				{
					Name:         "url",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_URL",
					Description:  "base URL of the UTxO RPC provider (e.g. https://utxorpc-mainnet.demeter.run)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.url),
				},
				{
					Name:         "mode",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_MODE",
					Description:  "streaming mode: follow-tip (blocks) or watch-tx (transactions)",
					DefaultValue: "follow-tip",
					Dest:         &(cmdlineOptions.mode),
				},
				{
					Name:         "network",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_NETWORK",
					Description:  "Cardano network name (mainnet, preprod, preview, sanchonet) for resolving network magic",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.network),
				},
				{
					Name:         "api-key-header",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_API_KEY_HEADER",
					Description:  "HTTP header name used for API key authentication (e.g. dmtr-api-key)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.apiKeyHeader),
				},
				{
					Name:         "api-key",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_API_KEY",
					Description:  "API key value used for authentication",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.apiKey),
				},
				{
					Name:         "intersect-tip",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "start streaming from the current chain tip",
					DefaultValue: true,
					Dest:         &(cmdlineOptions.intersectTip),
				},
				{
					Name:         "intersect-point",
					Type:         plugin.PluginOptionTypeString,
					Description:  "explicit intersect point(s) in '<slot>.<hash>' or 'slot1.hash1,slot2.hash2' format",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.intersectPoint),
				},
				{
					Name:         "auto-reconnect",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "automatically reconnect if the UTxO RPC stream fails",
					DefaultValue: true,
					Dest:         &(cmdlineOptions.autoReconnect),
				},
				{
					Name:         "include-cbor",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "include raw CBOR when available in emitted events",
					DefaultValue: false,
					Dest:         &(cmdlineOptions.includeCbor),
				},
			},
		},
	)
}

// NewFromCmdlineOptions constructs the plugin from registered command-line options.
func NewFromCmdlineOptions() plugin.Plugin {
	opts := []UtxoRpcOptionFunc{
		WithLogger(
			logging.GetLogger().With("plugin", "input.utxorpc"),
		),
		WithURL(cmdlineOptions.url),
		WithMode(cmdlineOptions.mode),
		WithNetwork(cmdlineOptions.network),
		WithAPIKeyHeader(cmdlineOptions.apiKeyHeader),
		WithAPIKey(cmdlineOptions.apiKey),
		WithIntersectTip(cmdlineOptions.intersectTip),
		WithIntersectPoint(cmdlineOptions.intersectPoint),
		WithAutoReconnect(cmdlineOptions.autoReconnect),
		WithIncludeCbor(cmdlineOptions.includeCbor),
	}
	return New(opts...)
}
