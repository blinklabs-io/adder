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

import (
	"encoding/hex"
	"strings"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

type CardanoOptionFunc func(*Cardano)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) CardanoOptionFunc {
	return func(c *Cardano) {
		c.logger = logger
	}
}

// WithAddresses pre-processes addresses into optimized lookup structures
func WithAddresses(addresses []string) CardanoOptionFunc {
	return func(c *Cardano) {
		if c.filterSet.addresses == nil {
			c.filterSet.addresses = &addressFilter{
				paymentAddresses:      make(map[string]struct{}),
				stakeAddresses:        make(map[string]struct{}),
				stakeCredentialHashes: make(map[string][]byte),
			}
		}

		for _, addr := range addresses {
			if strings.HasPrefix(addr, "stake") {
				c.filterSet.addresses.stakeAddresses[addr] = struct{}{}
				// Pre-decode the bech32 to get credential hash
				if _, data, err := bech32.Decode(addr); err == nil {
					if decoded, err := bech32.ConvertBits(data, 5, 8, false); err == nil {
						c.filterSet.addresses.stakeCredentialHashes[addr] = decoded
					}
				}
			} else {
				c.filterSet.addresses.paymentAddresses[addr] = struct{}{}
			}
		}
		c.filterSet.hasAddressFilter = true
	}
}

// WithPoolIds pre-computes both hex and bech32 representations
func WithPoolIds(poolIds []string) CardanoOptionFunc {
	return func(c *Cardano) {
		if c.filterSet.pools == nil {
			c.filterSet.pools = &poolFilter{
				hexPoolIds:    make(map[string]struct{}),
				bech32PoolIds: make(map[string]struct{}),
				hexToBech32:   make(map[string]string),
				bytesPoolIds:  make(map[string][]byte),
			}
		}

		for _, poolId := range poolIds {
			if strings.HasPrefix(poolId, "pool") {
				// It's bech32 format - always store the original
				c.filterSet.pools.bech32PoolIds[poolId] = struct{}{}
				// Try to decode and compute hex representation
				if _, data, err := bech32.Decode(poolId); err == nil {
					if decoded, err := bech32.ConvertBits(data, 5, 8, false); err == nil {
						hexId := hex.EncodeToString(decoded)
						c.filterSet.pools.hexPoolIds[hexId] = struct{}{}
						c.filterSet.pools.hexToBech32[hexId] = poolId
						// Pre-compute byte slice for direct comparison
						c.filterSet.pools.bytesPoolIds[hexId] = decoded
					}
				}
			} else {
				// It's hex format - store and compute bech32
				c.filterSet.pools.hexPoolIds[poolId] = struct{}{}
				if hexBytes, err := hex.DecodeString(poolId); err == nil {
					// Pre-compute byte slice for direct comparison
					c.filterSet.pools.bytesPoolIds[poolId] = hexBytes
					if convData, err := bech32.ConvertBits(hexBytes, 8, 5, true); err == nil {
						if encoded, err := bech32.Encode("pool", convData); err == nil {
							c.filterSet.pools.bech32PoolIds[encoded] = struct{}{}
							c.filterSet.pools.hexToBech32[poolId] = encoded
						}
					}
				}
			}
		}
		c.filterSet.hasPoolFilter = true
	}
}

// WithPolicies creates O(1) lookup map for policy IDs
func WithPolicies(policyIds []string) CardanoOptionFunc {
	return func(c *Cardano) {
		if c.filterSet.policies == nil {
			c.filterSet.policies = &policyFilter{
				policyIds: make(map[string]struct{}),
			}
		}
		for _, policyId := range policyIds {
			c.filterSet.policies.policyIds[policyId] = struct{}{}
		}
		c.filterSet.hasPolicyFilter = true
	}
}

// WithAssetFingerprints creates O(1) lookup map for asset fingerprints
func WithAssetFingerprints(fingerprints []string) CardanoOptionFunc {
	return func(c *Cardano) {
		if c.filterSet.assets == nil {
			c.filterSet.assets = &assetFilter{
				fingerprints: make(map[string]struct{}),
			}
		}
		for _, fp := range fingerprints {
			c.filterSet.assets.fingerprints[fp] = struct{}{}
		}
		c.filterSet.hasAssetFilter = true
	}
}

// WithDRepIds pre-computes both hex and bech32 representations for DRep filtering
func WithDRepIds(drepIds []string) CardanoOptionFunc {
	return func(c *Cardano) {
		if c.filterSet.dreps == nil {
			c.filterSet.dreps = &drepFilter{
				hexDRepIds:    make(map[string]struct{}),
				bech32DRepIds: make(map[string]struct{}),
				hexToBech32:   make(map[string]string),
				bytesDRepIds:  make(map[string][]byte),
			}
		}

		for _, drepId := range drepIds {
			if drepId == "" {
				continue
			}

			if strings.HasPrefix(drepId, "drep") {
				// bech32 format (drep1xxx or drep_script1xxx)
				c.filterSet.dreps.bech32DRepIds[drepId] = struct{}{}
				// Try to decode and compute hex representation
				if _, data, err := bech32.Decode(drepId); err == nil {
					if decoded, err := bech32.ConvertBits(data, 5, 8, false); err == nil {
						hexId := hex.EncodeToString(decoded)
						c.filterSet.dreps.hexDRepIds[hexId] = struct{}{}
						c.filterSet.dreps.hexToBech32[hexId] = drepId
						// Pre-compute byte slice for direct comparison
						c.filterSet.dreps.bytesDRepIds[hexId] = decoded
					}
				}
			} else {
				// Assume hex format - store hex
				c.filterSet.dreps.hexDRepIds[drepId] = struct{}{}
				// Compute both bech32 variants (drep and drep_script)
				if hexBytes, err := hex.DecodeString(drepId); err == nil {
					// Pre-compute byte slice for direct comparison
					c.filterSet.dreps.bytesDRepIds[drepId] = hexBytes
					if convData, err := bech32.ConvertBits(hexBytes, 8, 5, true); err == nil {
						// Store as key hash version
						if encoded, err := bech32.Encode("drep", convData); err == nil {
							c.filterSet.dreps.bech32DRepIds[encoded] = struct{}{}
							c.filterSet.dreps.hexToBech32[drepId] = encoded
						}
						// Also store as script hash version
						if encoded, err := bech32.Encode("drep_script", convData); err == nil {
							c.filterSet.dreps.bech32DRepIds[encoded] = struct{}{}
						}
					}
				}
			}
		}
		c.filterSet.hasDRepFilter = true
	}
}
