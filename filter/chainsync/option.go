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

import "github.com/blinklabs-io/snek/plugin"

type ChainSyncOptionFunc func(*ChainSync)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.logger = logger
	}
}

// WithAddresses specfies the address to filter on
func WithAddresses(addresses []string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterAddresses = addresses[:]
	}
}

// WithAssetFingerprints specifies the asset fingerprint (asset1xxx) to filter on
func WithAssetFingerprints(assetFingerprints []string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterAssetFingerprints = assetFingerprints[:]
	}
}

// WithPolicies specfies the address to filter on
func WithPolicies(policyIds []string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterPolicyIds = policyIds[:]
	}
}

// WithPoolIds specifies the pool to filter on
func WithPoolIds(poolIds []string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterPoolIds = poolIds[:]
	}
}
