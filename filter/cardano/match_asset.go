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

package cardano

import (
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/ledger"
)

// matchPolicyFilter checks if transaction matches policy ID filters
func (c *Cardano) matchPolicyFilter(te event.TransactionEvent) bool {
	// Include resolved inputs as outputs for matching
	allOutputs := make([]ledger.TransactionOutput, 0, len(te.Outputs)+len(te.ResolvedInputs))
	allOutputs = append(allOutputs, te.Outputs...)
	allOutputs = append(allOutputs, te.ResolvedInputs...)

	for _, output := range allOutputs {
		if output.Assets() != nil {
			for _, policyId := range output.Assets().Policies() {
				// O(1) lookup in policy IDs
				if _, exists := c.filterSet.policies.policyIds[policyId.String()]; exists {
					return true
				}
			}
		}
	}
	return false
}

// matchAssetFilter checks if transaction matches asset fingerprint filters
func (c *Cardano) matchAssetFilter(te event.TransactionEvent) bool {
	// Include resolved inputs as outputs for matching
	allOutputs := make([]ledger.TransactionOutput, 0, len(te.Outputs)+len(te.ResolvedInputs))
	allOutputs = append(allOutputs, te.Outputs...)
	allOutputs = append(allOutputs, te.ResolvedInputs...)

	for _, output := range allOutputs {
		if output.Assets() != nil {
			for _, policyId := range output.Assets().Policies() {
				for _, assetName := range output.Assets().Assets(policyId) {
					assetFp := ledger.NewAssetFingerprint(policyId.Bytes(), assetName)
					// O(1) lookup in asset fingerprints
					if _, exists := c.filterSet.assets.fingerprints[assetFp.String()]; exists {
						return true
					}
				}
			}
		}
	}
	return false
}
