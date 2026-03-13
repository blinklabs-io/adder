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

import "github.com/blinklabs-io/adder/plugin"

// UtxoRpcOptionFunc is a functional option for configuring the UTxO RPC input plugin.
type UtxoRpcOptionFunc func(*Utxorpc)

// WithLogger sets the logger used by the plugin.
func WithLogger(logger plugin.Logger) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.logger = logger
	}
}

// WithNetwork sets the Cardano network name (e.g. "mainnet", "preprod")
// used to resolve the network magic for event context.
func WithNetwork(network string) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.network = network
	}
}

// WithURL sets the base URL of the UTxO RPC provider.
func WithURL(url string) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.url = url
	}
}

// WithMode sets the streaming mode (follow-tip or watch-tx).
func WithMode(mode string) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.mode = mode
	}
}

// WithAPIKeyHeader sets the header name used for API key authentication.
func WithAPIKeyHeader(header string) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.apiKeyHeader = header
	}
}

// WithAPIKey sets the API key value.
func WithAPIKey(key string) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.apiKey = key
	}
}

// WithIntersectTip controls whether the stream should intersect at the chain tip.
func WithIntersectTip(intersectTip bool) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.intersectTip = intersectTip
	}
}

// WithIntersectPoint sets an explicit intersect point string (slot.hash or comma-separated list).
func WithIntersectPoint(point string) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.intersectPoint = point
	}
}

// WithAutoReconnect controls whether the plugin should auto-reconnect on stream failures.
func WithAutoReconnect(autoReconnect bool) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.autoReconnect = autoReconnect
	}
}

// WithIncludeCbor controls whether CBOR bytes should be included in emitted events where available.
func WithIncludeCbor(includeCbor bool) UtxoRpcOptionFunc {
	return func(u *Utxorpc) {
		u.includeCbor = includeCbor
	}
}
