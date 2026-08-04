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
	"bytes"
	"encoding/hex"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
)

// matchAddressFilter checks if transaction matches address filters
func (c *Cardano) matchAddressFilter(te event.TransactionEvent) bool {
	// Include resolved inputs as outputs for matching
	allOutputs := make([]ledger.TransactionOutput, 0, len(te.Outputs)+len(te.ResolvedInputs))
	allOutputs = append(allOutputs, te.Outputs...)
	allOutputs = append(allOutputs, te.ResolvedInputs...)

	// Check outputs against payment and stake addresses
	for _, output := range allOutputs {
		addrStr := output.Address().String()

		// O(1) lookup in payment addresses
		if _, exists := c.filterSet.addresses.paymentAddresses[addrStr]; exists {
			return true
		}

		// Check stake address if we have stake filters
		if len(c.filterSet.addresses.stakeAddresses) > 0 {
			stakeAddr := output.Address().StakeAddress()
			if stakeAddr != nil {
				// O(1) lookup in stake addresses
				if _, exists := c.filterSet.addresses.stakeAddresses[stakeAddr.String()]; exists {
					return true
				}
			}
		}
	}

	// Check certificates for stake address matches
	if len(c.filterSet.addresses.stakeAddresses) > 0 {
		if c.matchStakeCertificates(te.Certificates) {
			return true
		}
	}

	return false
}

// matchAddressFilterGovernance checks if governance event matches address filters
func (c *Cardano) matchAddressFilterGovernance(ge event.GovernanceEvent) bool {
	// Check proposal procedures for reward account matches
	for _, prop := range ge.ProposalProcedures {
		// RewardAccount is a stake/reward address string
		if _, exists := c.filterSet.addresses.stakeAddresses[prop.RewardAccount]; exists {
			return true
		}

		// Check treasury withdrawal addresses if this is a treasury withdrawal action
		if prop.ActionData.TreasuryWithdrawal != nil {
			for _, withdrawal := range prop.ActionData.TreasuryWithdrawal.Withdrawals {
				// Check against payment addresses
				if _, exists := c.filterSet.addresses.paymentAddresses[withdrawal.Address]; exists {
					return true
				}
				// Also check against stake addresses (some withdrawals may use stake addresses)
				if _, exists := c.filterSet.addresses.stakeAddresses[withdrawal.Address]; exists {
					return true
				}
			}
		}
	}

	// Check vote delegation certificates for stake credential matches
	if len(c.filterSet.addresses.stakeCredentialHashes) > 0 {
		for _, cert := range ge.VoteDelegationCertificates {
			// StakeCredential is a hex string of the credential hash (28 bytes)
			credBytes, err := hex.DecodeString(cert.StakeCredential)
			if err != nil {
				continue
			}
			if c.matchStakeCredentialHash(credBytes) {
				return true
			}
		}
	}

	return false
}

// matchStakeCertificates checks certificates against stake credential hashes
func (c *Cardano) matchStakeCertificates(certificates []ledger.Certificate) bool {
	for _, certificate := range certificates {
		var credBytes []byte
		switch cert := certificate.(type) {
		case *common.StakeDelegationCertificate:
			if cert.StakeCredential != nil {
				hash := cert.StakeCredential.Hash()
				credBytes = hash[:]
			}
		case *common.StakeDeregistrationCertificate:
			hash := cert.StakeCredential.Hash()
			credBytes = hash[:]
		default:
			continue
		}

		if credBytes == nil {
			continue
		}

		if c.matchStakeCredentialHash(credBytes) {
			return true
		}
	}
	return false
}

// matchStakeCredentialHash compares a decoded credential hash against configured stake credential hashes
func (c *Cardano) matchStakeCredentialHash(credBytes []byte) bool {
	for _, filterHash := range c.filterSet.addresses.stakeCredentialHashes {
		// filterHash may include header byte from bech32 decoding
		// Compare against last 28 bytes (the actual credential hash)
		var hashToCompare []byte
		if len(filterHash) > 28 {
			hashToCompare = filterHash[len(filterHash)-28:]
		} else {
			hashToCompare = filterHash
		}
		if bytes.Equal(credBytes, hashToCompare) {
			return true
		}
	}
	return false
}
