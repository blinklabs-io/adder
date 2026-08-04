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
	"github.com/blinklabs-io/gouroboros/ledger/common"
)

// matchDRepFilterGovernance checks if governance event contains matching DRep IDs
func (c *Cardano) matchDRepFilterGovernance(ge event.GovernanceEvent) bool {
	// Check DRep certificates (registrations, updates, retirements)
	for _, cert := range ge.DRepCertificates {
		if _, exists := c.filterSet.dreps.hexDRepIds[cert.DRepHash]; exists {
			return true
		}
	}

	// Check vote delegation certificates (delegations TO a DRep)
	for _, cert := range ge.VoteDelegationCertificates {
		if cert.DRepHash != "" {
			if _, exists := c.filterSet.dreps.hexDRepIds[cert.DRepHash]; exists {
				return true
			}
		}
	}

	// Check voting procedures (votes cast BY a DRep)
	for _, vote := range ge.VotingProcedures {
		if vote.VoterType == "DRep" {
			if _, exists := c.filterSet.dreps.hexDRepIds[vote.VoterHash]; exists {
				return true
			}
		}
	}

	return false
}

// matchDRepFilterTx checks transaction certificates against DRep filters
func (c *Cardano) matchDRepFilterTx(te event.TransactionEvent) bool {
	for _, certificate := range te.Certificates {
		var drepHash []byte

		switch cert := certificate.(type) {
		case *common.RegistrationDrepCertificate:
			drepHash = cert.DrepCredential.Credential[:]
		case *common.DeregistrationDrepCertificate:
			drepHash = cert.DrepCredential.Credential[:]
		case *common.UpdateDrepCertificate:
			drepHash = cert.DrepCredential.Credential[:]
		case *common.VoteDelegationCertificate:
			if cert.Drep.Type == common.DrepTypeAddrKeyHash ||
				cert.Drep.Type == common.DrepTypeScriptHash {
				drepHash = cert.Drep.Credential
			}
		case *common.StakeVoteDelegationCertificate:
			if cert.Drep.Type == common.DrepTypeAddrKeyHash ||
				cert.Drep.Type == common.DrepTypeScriptHash {
				drepHash = cert.Drep.Credential
			}
		case *common.VoteRegistrationDelegationCertificate:
			if cert.Drep.Type == common.DrepTypeAddrKeyHash ||
				cert.Drep.Type == common.DrepTypeScriptHash {
				drepHash = cert.Drep.Credential
			}
		case *common.StakeVoteRegistrationDelegationCertificate:
			if cert.Drep.Type == common.DrepTypeAddrKeyHash ||
				cert.Drep.Type == common.DrepTypeScriptHash {
				drepHash = cert.Drep.Credential
			}
		default:
			continue
		}

		if drepHash != nil {
			// O(1) lookup using byte string key (no encoding needed)
			if _, exists := c.filterSet.dreps.bytesLookup[string(drepHash)]; exists {
				return true
			}
		}
	}

	// Also check VotingProcedures from raw transaction if available
	if te.Transaction != nil {
		for voter := range te.Transaction.VotingProcedures() {
			if voter.Type == common.VoterTypeDRepKeyHash ||
				voter.Type == common.VoterTypeDRepScriptHash {
				voterHash := voter.Hash[:]
				// O(1) lookup using byte string key (no encoding needed)
				if _, exists := c.filterSet.dreps.bytesLookup[string(voterHash)]; exists {
					return true
				}
			}
		}
	}

	return false
}
