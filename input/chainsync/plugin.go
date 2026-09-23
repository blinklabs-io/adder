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

package chainsync

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeInput,
			Name:               "chainsync",
			Description:        "syncs blocks from a Cardano node using either NtC (node-to-client) or NtN (node-to-node)",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "kupo-url",
					Type:         plugin.PluginOptionTypeString,
					DefaultValue: "",
					CustomEnvVar: "KUPO_URL",
					Description:  "Kupo HTTP endpoint for resolving transaction inputs",
				},
				{
					Name:         "network",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NETWORK",
					Description:  "specifies a well-known Cardano network name",
					DefaultValue: "mainnet",
				},
				{
					Name:         "network-magic",
					Type:         plugin.PluginOptionTypeUint,
					Description:  "specifies the network magic value to use, overrides 'network'",
					DefaultValue: uint(0),
				},
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the TCP address of the node to connect to in the form 'host:port'",
					DefaultValue: "",
				},
				{
					Name:         "socket-path",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NODE_SOCKET_PATH",
					Description:  "specifies the path to the UNIX socket to connect to",
					DefaultValue: "",
				},
				{
					Name:         "ntc-tcp",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "use the NtC (node-to-client) protocol over TCP, for use when exposing a node's UNIX socket via socat or similar",
					DefaultValue: false,
				},
				{
					Name:         "intersect-tip",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "start syncing at the chain tip (defaults to chain genesis)",
					DefaultValue: true,
				},
				{
					Name:         "intersect-point",
					Type:         plugin.PluginOptionTypeString,
					Description:  "start syncing at the specified chain point(s) in '<slot>.<hash>' format",
					DefaultValue: "",
				},
				{
					Name:         "include-cbor",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "include original CBOR for block/transaction in events",
					DefaultValue: false,
				},
				{
					Name:         "auto-reconnect",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "auto-reconnect if the connection is broken",
					DefaultValue: true,
				},
				{
					Name:         "delay-confirmations",
					Type:         plugin.PluginOptionTypeUint,
					Description:  "number of confirmations required before emitting events",
					DefaultValue: uint(0),
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
	}
	if values.String("network") == "" && values.String("address") == "" &&
		values.String("socket-path") == "" {
		return nil, errors.New("network, address, or socket-path is required")
	}

	//nolint:gosec // Options validates every uint as an unsigned 32-bit value.
	nm := uint32(values.Uint("network-magic"))
	opts := []ChainSyncOptionFunc{
		WithLogger(
			logging.GetLogger().With("plugin", "input.chainsync"),
		),
		WithNetwork(values.String("network")),
		WithKupoUrl(values.String("kupo-url")),
		WithNetworkMagic(nm),
		WithAddress(values.String("address")),
		WithSocketPath(values.String("socket-path")),
		WithNtcTcp(values.Bool("ntc-tcp")),
		WithIncludeCbor(values.Bool("include-cbor")),
		WithAutoReconnect(values.Bool("auto-reconnect")),
		WithDelayConfirmations(values.Uint("delay-confirmations")),
	}
	pointsSlice := plugin.SplitAndTrim(values.String("intersect-point"))
	if len(pointsSlice) > 0 {
		intersectPoints := make([]ocommon.Point, 0, len(pointsSlice))
		for _, point := range pointsSlice {
			intersectPointParts := strings.Split(point, ".")
			if len(intersectPointParts) != 2 {
				return nil, errors.New(
					"invalid intersect point format: expected '<slot>.<hash>'",
				)
			}
			intersectSlot, err := strconv.ParseUint(
				intersectPointParts[0],
				10,
				64,
			)
			if err != nil {
				return nil, errors.New(
					"invalid intersect point format: slot must be a number",
				)
			}
			intersectHashBytes, err := hex.DecodeString(intersectPointParts[1])
			if err != nil || len(intersectHashBytes) != 32 {
				return nil, errors.New(
					"invalid intersect point format: hash must be 32 bytes of hex",
				)
			}
			intersectPoints = append(
				intersectPoints,
				ocommon.Point{
					Slot: intersectSlot,
					Hash: intersectHashBytes[:],
				},
			)
		}
		opts = append(
			opts,
			WithIntersectPoints(intersectPoints),
		)
	} else {
		opts = append(
			opts,
			WithIntersectTip(values.Bool("intersect-tip")),
		)
	}
	p := New(opts...)
	return p, nil
}
