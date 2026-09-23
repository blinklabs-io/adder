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

package mempool

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeInput,
			Name:               "mempool",
			Description:        "reads unconfirmed transactions from a Cardano node's mempool via LocalTxMonitor (NtC)",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "network",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NETWORK",
					Description:  "well-known Cardano network name (e.g. mainnet, preprod)",
					DefaultValue: "mainnet",
				},
				{
					Name:         "network-magic",
					Type:         plugin.PluginOptionTypeUint,
					Description:  "network magic value (overrides network name)",
					DefaultValue: uint(0),
				},
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "TCP address (host:port); requires ntc-tcp=true",
					DefaultValue: "",
				},
				{
					Name:         "socket-path",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NODE_SOCKET_PATH",
					Description:  "path to the node's UNIX socket (NtC)",
					DefaultValue: "",
				},
				{
					Name:         "ntc-tcp",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "use NtC over TCP (e.g. when exposing socket via socat)",
					DefaultValue: false,
				},
				{
					Name:         "include-cbor",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "include transaction CBOR in events",
					DefaultValue: false,
				},
				{
					Name:         "poll-interval",
					Type:         plugin.PluginOptionTypeString,
					Description:  "how often to poll the mempool (e.g. 5s, 1m)",
					DefaultValue: "5s",
				},
				{
					Name:         "kupo-url",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "KUPO_URL",
					Description:  "Kupo API URL for resolving transaction inputs (e.g. http://localhost:1442). Kupo must index the outputs you need (e.g. run with --match \"*\") or resolution will be empty.",
					DefaultValue: "",
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	if endpoint := values.String("kupo-url"); endpoint != "" {
		if err := plugin.ValidateHTTPURL(endpoint); err != nil {
			return nil, fmt.Errorf("kupo-url: %w", err)
		}
	}

	if values.String("network") != "" {
		if _, ok := ouroboros.NetworkByName(values.String("network")); !ok {
			return nil, errors.New("unknown network")
		}
	}
	if values.String("address") != "" {
		if _, _, err := net.SplitHostPort(values.String("address")); err != nil {
			return nil, errors.New("address must be host:port")
		}
		if !values.Bool("ntc-tcp") {
			return nil, errors.New("address requires ntc-tcp=true")
		}
	}
	if values.String("address") == "" && values.String("socket-path") == "" {
		return nil, errors.New("address or socket-path is required")
	}
	duration, err := time.ParseDuration(values.String("poll-interval"))
	if err != nil || duration <= 0 {
		return nil, errors.New("poll-interval must be a positive duration")
	}
	if values.String("network") == "" && values.Uint("network-magic") == 0 {
		return nil, errors.New("network or network-magic is required")
	}

	//nolint:gosec // Options validates every uint as an unsigned 32-bit value.
	nm := uint32(values.Uint("network-magic"))
	return New(
		WithLogger(
			logging.GetLogger().With("plugin", "input.mempool"),
		),
		WithNetwork(values.String("network")),
		WithNetworkMagic(nm),
		WithAddress(values.String("address")),
		WithSocketPath(values.String("socket-path")),
		WithNtcTcp(values.Bool("ntc-tcp")),
		WithIncludeCbor(values.Bool("include-cbor")),
		WithPollInterval(values.String("poll-interval")),
		WithKupoUrl(values.String("kupo-url")),
	), nil
}
