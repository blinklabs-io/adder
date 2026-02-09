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
	"math"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var cmdlineOptions struct {
	network      string
	address      string
	socketPath   string
	networkMagic uint
	ntcTcp       bool
	includeCbor  bool
	pollInterval string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeInput,
			Name:               "mempool",
			Description:        "reads unconfirmed transactions from a Cardano node's mempool via LocalTxMonitor (NtC)",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "network",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NETWORK",
					Description:  "well-known Cardano network name (e.g. mainnet, preprod)",
					DefaultValue: "mainnet",
					Dest:         &(cmdlineOptions.network),
				},
				{
					Name:         "network-magic",
					Type:         plugin.PluginOptionTypeUint,
					Description:  "network magic value (overrides network name)",
					DefaultValue: uint(0),
					Dest:         &(cmdlineOptions.networkMagic),
				},
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "TCP address (host:port); requires ntc-tcp=true",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.address),
				},
				{
					Name:         "socket-path",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NODE_SOCKET_PATH",
					Description:  "path to the node's UNIX socket (NtC)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.socketPath),
				},
				{
					Name:         "ntc-tcp",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "use NtC over TCP (e.g. when exposing socket via socat)",
					DefaultValue: false,
					Dest:         &(cmdlineOptions.ntcTcp),
				},
				{
					Name:         "include-cbor",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "include transaction CBOR in events",
					DefaultValue: false,
					Dest:         &(cmdlineOptions.includeCbor),
				},
				{
					Name:         "poll-interval",
					Type:         plugin.PluginOptionTypeString,
					Description:  "how often to poll the mempool (e.g. 5s, 1m)",
					DefaultValue: "5s",
					Dest:         &(cmdlineOptions.pollInterval),
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	var nm uint32
	if cmdlineOptions.networkMagic > 0 && cmdlineOptions.networkMagic <= math.MaxUint32 {
		nm = uint32(cmdlineOptions.networkMagic)
	}
	return New(
		WithLogger(
			logging.GetLogger().With("plugin", "input.mempool"),
		),
		WithNetwork(cmdlineOptions.network),
		WithNetworkMagic(nm),
		WithAddress(cmdlineOptions.address),
		WithSocketPath(cmdlineOptions.socketPath),
		WithNtcTcp(cmdlineOptions.ntcTcp),
		WithIncludeCbor(cmdlineOptions.includeCbor),
		WithPollInterval(cmdlineOptions.pollInterval),
	)
}
