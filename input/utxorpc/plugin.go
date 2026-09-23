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
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeInput,
			Name:               "utxorpc",
			Description:        "reads blocks and transactions from a UTxO RPC provider over gRPC streaming endpoints",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "url",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_URL",
					Description:  "base URL of the UTxO RPC provider (e.g. https://utxorpc-mainnet.demeter.run)",
					DefaultValue: "",
				},
				{
					Name:         "mode",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_MODE",
					Description:  "streaming mode: follow-tip (blocks) or watch-tx (transactions)",
					DefaultValue: "follow-tip",
				},
				{
					Name:         "network",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_NETWORK",
					Description:  "Cardano network name (mainnet, preprod, preview, sanchonet) for resolving network magic",
					DefaultValue: "",
				},
				{
					Name:         "api-key-header",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_API_KEY_HEADER",
					Description:  "HTTP header name used for API key authentication (e.g. dmtr-api-key)",
					DefaultValue: "",
				},
				{
					Name:         "api-key",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "UTXORPC_API_KEY",
					Description:  "API key value used for authentication",
					DefaultValue: "",
				},
				{
					Name:         "intersect-tip",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "start streaming from the current chain tip",
					DefaultValue: true,
				},
				{
					Name:         "intersect-point",
					Type:         plugin.PluginOptionTypeString,
					Description:  "explicit intersect point(s) in '<slot>.<hash>' or 'slot1.hash1,slot2.hash2' format",
					DefaultValue: "",
				},
				{
					Name:         "auto-reconnect",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "automatically reconnect if the UTxO RPC stream fails",
					DefaultValue: true,
				},
				{
					Name:         "include-cbor",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "include raw CBOR when available in emitted events",
					DefaultValue: false,
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	if err := plugin.ValidateHTTPURL(values.String("url")); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	if values.String("mode") != modeFollowTip &&
		values.String("mode") != modeWatchTx {
		return nil, errors.New("mode must be follow-tip or watch-tx")
	}
	if values.String("network") != "" {
		if _, ok := ouroboros.NetworkByName(values.String("network")); !ok {
			return nil, errors.New("unknown network")
		}
	}
	for _, point := range plugin.SplitAndTrim(values.String("intersect-point")) {
		parts := strings.Split(point, ".")
		if len(parts) != 2 {
			return nil, errors.New("intersect-point must be slot.hash")
		}
		if _, err := strconv.ParseUint(parts[0], 10, 64); err != nil {
			return nil, errors.New(
				"intersect-point slot must be an unsigned integer",
			)
		}
		hash, err := hex.DecodeString(parts[1])
		if err != nil || len(hash) != 32 {
			return nil, errors.New(
				"intersect-point hash must be 32 bytes of hex",
			)
		}
	}
	if (values.String("api-key-header") == "") != (values.String("api-key") == "") {
		return nil, errors.New(
			"api-key and api-key-header must be supplied together",
		)
	}

	opts := []UtxoRpcOptionFunc{
		WithLogger(
			logging.GetLogger().With("plugin", "input.utxorpc"),
		),
		WithURL(values.String("url")),
		WithMode(values.String("mode")),
		WithNetwork(values.String("network")),
		WithAPIKeyHeader(values.String("api-key-header")),
		WithAPIKey(values.String("api-key")),
		WithIntersectTip(values.Bool("intersect-tip")),
		WithIntersectPoint(values.String("intersect-point")),
		WithAutoReconnect(values.Bool("auto-reconnect")),
		WithIncludeCbor(values.Bool("include-cbor")),
	}
	return New(opts...), nil
}
