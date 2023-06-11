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

type ChainSyncOptionFunc func(*ChainSync)

// WithAddress specfies the address to filter on
func WithAddress(address string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterAddress = address
	}
}

// WithPolicy specfies the address to filter on
func WithPolicy(policyId string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterPolicyId = policyId
	}
}

//WithAssetFingerprint specifies the asset fingerprint (asset1xxx) to filter on
func WithAssetFingerprint(assetFingerprint string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.filterAssetFingerprint = assetFingerprint
	}
}
