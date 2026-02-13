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
	"github.com/blinklabs-io/adder/plugin"
)

type MempoolOptionFunc func(*Mempool)

// WithLogger specifies the logger to use
func WithLogger(logger plugin.Logger) MempoolOptionFunc {
	return func(m *Mempool) {
		m.logger = logger
	}
}

// WithNetwork specifies the network name (e.g. mainnet, preprod)
func WithNetwork(network string) MempoolOptionFunc {
	return func(m *Mempool) {
		m.network = network
	}
}

// WithNetworkMagic specifies the network magic value (overrides network name)
func WithNetworkMagic(networkMagic uint32) MempoolOptionFunc {
	return func(m *Mempool) {
		m.networkMagic = networkMagic
	}
}

// WithSocketPath specifies the path to the node's UNIX socket (NtC)
func WithSocketPath(socketPath string) MempoolOptionFunc {
	return func(m *Mempool) {
		m.socketPath = socketPath
	}
}

// WithAddress specifies the TCP address (host:port). Use WithNtcTcp(true) for NtC over TCP.
func WithAddress(address string) MempoolOptionFunc {
	return func(m *Mempool) {
		m.address = address
	}
}

// WithNtcTcp specifies whether to use NtC over TCP (e.g. when exposing socket via socat)
func WithNtcTcp(ntcTcp bool) MempoolOptionFunc {
	return func(m *Mempool) {
		m.ntcTcp = ntcTcp
	}
}

// WithIncludeCbor specifies whether to include transaction CBOR in events
func WithIncludeCbor(includeCbor bool) MempoolOptionFunc {
	return func(m *Mempool) {
		m.includeCbor = includeCbor
	}
}

// WithPollInterval specifies how often to poll the mempool (e.g. "5s", "1m"). Default 5s.
func WithPollInterval(duration string) MempoolOptionFunc {
	return func(m *Mempool) {
		m.pollIntervalStr = duration
	}
}
