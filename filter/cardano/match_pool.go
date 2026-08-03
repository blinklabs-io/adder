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
	"encoding/hex"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
)

// matchPoolFilterGovernance checks if governance event contains matching pool IDs
func (c *Cardano) matchPoolFilterGovernance(ge event.GovernanceEvent) bool {
	// Check voting procedures (votes cast BY an SPO / pool)
	for _, vote := range ge.VotingProcedures {
		if vote.VoterType == "SPO" && vote.VoterHash != "" {
			if _, exists := c.filterSet.pools.hexPoolIds[vote.VoterHash]; exists {
				return true
			}
			// Also check bytes lookup for consistency with tx path (hex decode)
			if hexBytes, err := hex.DecodeString(vote.VoterHash); err == nil {
				if _, exists := c.filterSet.pools.bytesLookup[string(hexBytes)]; exists {
					return true
				}
			}
		}
	}

	// Check vote delegation certificates that reference a pool (PoolKeyHash)
	for _, cert := range ge.VoteDelegationCertificates {
		if cert.PoolKeyHash != "" {
			if _, exists := c.filterSet.pools.hexPoolIds[cert.PoolKeyHash]; exists {
				return true
			}
			if hexBytes, err := hex.DecodeString(cert.PoolKeyHash); err == nil {
				if _, exists := c.filterSet.pools.bytesLookup[string(hexBytes)]; exists {
					return true
				}
			}
		}
	}

	return false
}

// matchPoolFilterTx checks transaction certificates against pool filters
func (c *Cardano) matchPoolFilterTx(te event.TransactionEvent) bool {
	for _, certificate := range te.Certificates {
		var poolKeyHash []byte

		switch cert := certificate.(type) {
		case *ledger.StakeDelegationCertificate:
			poolKeyHash = cert.PoolKeyHash[:]
		case *ledger.PoolRetirementCertificate:
			poolKeyHash = cert.PoolKeyHash[:]
		case *ledger.PoolRegistrationCertificate:
			poolKeyHash = cert.Operator[:]
		default:
			continue
		}

		// O(1) lookup using byte string key (no encoding needed)
		if _, exists := c.filterSet.pools.bytesLookup[string(poolKeyHash)]; exists {
			return true
		}
	}

	// Also check VotingProcedures from raw transaction if available
	if te.Transaction != nil {
		for voter := range te.Transaction.VotingProcedures() {
			if voter.Type == common.VoterTypeStakingPoolKeyHash {
				poolKeyHash := voter.Hash[:]
				// O(1) lookup using byte string key (no encoding needed)
				if _, exists := c.filterSet.pools.bytesLookup[string(poolKeyHash)]; exists {
					return true
				}
			}
		}
	}

	return false
}
