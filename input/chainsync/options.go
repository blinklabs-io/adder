// Copyright 2023 Blink Labs Software
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
	"github.com/blinklabs-io/adder/plugin"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

type ChainSyncOptionFunc func(*ChainSync)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.logger = logger
	}
}

// WithNetwork specifies the network
func WithNetwork(network string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.network = network
	}
}

// WithNetworkMagic specifies the network magic value
func WithNetworkMagic(networkMagic uint32) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.networkMagic = networkMagic
	}
}

// WithNtcTcp specifies whether to use the NtC (node-to-client) protocol over TCP. This is useful when exposing a node's UNIX socket via socat or similar. The default is to use the NtN (node-to-node) protocol over TCP
func WithNtcTcp(ntcTcp bool) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.ntcTcp = ntcTcp
	}
}

// WithSocketPath specifies the socket path of the node to connect to
func WithSocketPath(socketPath string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.socketPath = socketPath
	}
}

// WithAddress specifies the TCP address of the node to connect to in the form "host:port"
func WithAddress(address string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.address = address
	}
}

// WithIntersectPoints specifies the point(s) to use when starting the ChainSync operation. The default is to start at the genesis of the blockchain
func WithIntersectPoints(points []ocommon.Point) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.intersectPoints = points
	}
}

// WithInterceptTip specifies whether to start the ChainSync operation from the chain tip. The default is to start at the genesis of the blockchain
func WithIntersectTip(intersectTip bool) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.intersectTip = intersectTip
	}
}

// WithIncludeCbor specifies whether to include the original CBOR for a block or transaction with the event
func WithIncludeCbor(includeCbor bool) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.includeCbor = includeCbor
	}
}

// WithAutoReconnect specified whether to automatically reconnect if the connection is broken
func WithAutoReconnect(autoReconnect bool) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.autoReconnect = autoReconnect
	}
}

// WithStatusUpdateFunc specifies a callback function for status updates. This is useful for tracking the chain-sync status
// to be able to resume a sync at a later time, especially when any filtering could prevent you from getting all block update events
func WithStatusUpdateFunc(
	statusUpdateFunc StatusUpdateFunc,
) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.statusUpdateFunc = statusUpdateFunc
	}
}

// WithBulkMode specifies whether to use the "bulk" sync mode with NtN (node-to-node). This should only be used against your own nodes for resource usage reasons
//
// Deprecated: this flag no longer does anything useful, as bulk mode is now the default (and only) mode of operation
func WithBulkMode(bulkMode bool) ChainSyncOptionFunc {
	return func(c *ChainSync) {}
}

// WithDelayConfirmationCount specifies the number of confirmations (subsequent blocks) are required before an event will be emitted
func WithDelayConfirmations(count uint) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.delayConfirmations = count
	}
}

// WithReconnectCallback specifies a function to call after a successful
// auto-reconnect. This allows consumers to detect reconnection events
// and take action (e.g., logging, re-syncing state, resetting watchdogs).
func WithReconnectCallback(callback func()) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.reconnectCallback = callback
	}
}
