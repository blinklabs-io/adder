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

package cardano

// filterSet holds all pre-computed filters for O(1) lookups
type filterSet struct {
	addresses *addressFilter
	pools     *poolFilter
	policies  *policyFilter
	assets    *assetFilter
	dreps     *drepFilter

	hasAddressFilter bool
	hasPoolFilter    bool
	hasPolicyFilter  bool
	hasAssetFilter   bool
	hasDRepFilter    bool
}

// addressFilter holds pre-computed address data for O(1) lookups
type addressFilter struct {
	paymentAddresses      map[string]struct{} // Full payment addresses
	stakeAddresses        map[string]struct{} // Stake addresses (prefix "stake")
	stakeCredentialHashes map[string][]byte   // Pre-decoded credential hashes for certificate matching
}

// poolFilter holds pre-computed pool ID data in both formats
type poolFilter struct {
	hexPoolIds    map[string]struct{} // Pool IDs in hex format
	bech32PoolIds map[string]struct{} // Pool IDs in bech32 format (pool1xxx)
	hexToBech32   map[string]string   // Maps hex -> bech32
}

// policyFilter holds policy IDs for O(1) lookup
type policyFilter struct {
	policyIds map[string]struct{}
}

// assetFilter holds asset fingerprints for O(1) lookup
type assetFilter struct {
	fingerprints map[string]struct{}
}

// drepFilter holds pre-computed DRep ID data for O(1) lookups
type drepFilter struct {
	hexDRepIds    map[string]struct{} // DRep IDs in hex format (primary lookup)
	bech32DRepIds map[string]struct{} // DRep IDs in bech32 format (drep1xxx, drep_script1xxx)
	hexToBech32   map[string]string   // Maps hex -> bech32 for reference
}
