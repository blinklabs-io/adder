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
	"bytes"
	"encoding/hex"
	"sync"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
)

type Cardano struct {
	errorChan  chan error
	inputChan  chan event.Event
	outputChan chan event.Event
	doneChan   chan struct{}
	wg         sync.WaitGroup
	stopOnce   sync.Once
	logger     plugin.Logger
	filterSet  filterSet
}

// New returns a new Cardano object with the specified options applied
func New(options ...CardanoOptionFunc) *Cardano {
	c := &Cardano{}
	for _, option := range options {
		option(c)
	}
	return c
}

// Start the cardano filter
func (c *Cardano) Start() error {
	c.errorChan = make(chan error)
	c.inputChan = make(chan event.Event, 10)
	c.outputChan = make(chan event.Event, 10)
	c.doneChan = make(chan struct{})
	c.stopOnce = sync.Once{}
	c.wg.Add(1)
	go c.processEvents()
	return nil
}

// processEvents handles incoming events and applies filters
func (c *Cardano) processEvents() {
	defer c.wg.Done()
	for {
		select {
		case <-c.doneChan:
			return
		case evt, ok := <-c.inputChan:
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			if c.filterEvent(evt) {
				// Send event along, but check for shutdown
				select {
				case <-c.doneChan:
					return
				case c.outputChan <- evt:
				}
			}
		}
	}
}

// filterEvent returns true if the event should be passed through
func (c *Cardano) filterEvent(evt event.Event) bool {
	switch v := evt.Payload.(type) {
	case event.BlockEvent:
		return c.filterBlockEvent(v)
	case event.TransactionEvent:
		return c.filterTransactionEvent(v)
	case event.GovernanceEvent:
		return c.filterGovernanceEvent(v)
	default:
		// Pass through events we don't filter
		return true
	}
}

// filterBlockEvent checks pool filter for block events using O(1) lookup
func (c *Cardano) filterBlockEvent(be event.BlockEvent) bool {
	if !c.filterSet.hasPoolFilter {
		return true
	}

	// O(1) lookup using pre-computed hexToBech32 map
	// Check if the issuer vkey (hex) maps to a filtered pool
	if _, exists := c.filterSet.pools.hexToBech32[be.IssuerVkey]; exists {
		return true
	}

	// Also check direct hex match in hexPoolIds
	if _, exists := c.filterSet.pools.hexPoolIds[be.IssuerVkey]; exists {
		return true
	}

	// Also check direct match in bech32PoolIds for bech32 format pool IDs
	if _, exists := c.filterSet.pools.bech32PoolIds[be.IssuerVkey]; exists {
		return true
	}

	return false
}

// filterTransactionEvent checks all applicable filters with early exit on match
func (c *Cardano) filterTransactionEvent(te event.TransactionEvent) bool {
	// Check address filter
	if c.filterSet.hasAddressFilter {
		if !c.matchAddressFilter(te) {
			return false
		}
	}

	// Check policy ID filter
	if c.filterSet.hasPolicyFilter {
		if !c.matchPolicyFilter(te) {
			return false
		}
	}

	// Check asset fingerprint filter
	if c.filterSet.hasAssetFilter {
		if !c.matchAssetFilter(te) {
			return false
		}
	}

	// Check pool filter
	if c.filterSet.hasPoolFilter {
		if !c.matchPoolFilterTx(te) {
			return false
		}
	}

	// Check DRep filter
	if c.filterSet.hasDRepFilter {
		if !c.matchDRepFilterTx(te) {
			return false
		}
	}

	return true
}

// matchAddressFilter checks if transaction matches address filters
func (c *Cardano) matchAddressFilter(te event.TransactionEvent) bool {
	// Include resolved inputs as outputs for matching
	allOutputs := append(te.Outputs, te.ResolvedInputs...)

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

// matchStakeCertificates checks certificates against stake credential hashes
func (c *Cardano) matchStakeCertificates(certificates []ledger.Certificate) bool {
	for _, certificate := range certificates {
		var credBytes []byte
		switch cert := certificate.(type) {
		case *common.StakeDelegationCertificate:
			hash := cert.StakeCredential.Hash()
			credBytes = hash[:]
		case *common.StakeDeregistrationCertificate:
			hash := cert.StakeCredential.Hash()
			credBytes = hash[:]
		default:
			continue
		}

		// Use pre-decoded stake credential hashes with bytes.Equal comparison
		for _, filterHash := range c.filterSet.addresses.stakeCredentialHashes {
			if bytes.Equal(credBytes, filterHash) {
				return true
			}
		}
	}
	return false
}

// matchPolicyFilter checks if transaction matches policy ID filters
func (c *Cardano) matchPolicyFilter(te event.TransactionEvent) bool {
	// Include resolved inputs as outputs for matching
	allOutputs := append(te.Outputs, te.ResolvedInputs...)

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
	allOutputs := append(te.Outputs, te.ResolvedInputs...)

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

// filterGovernanceEvent checks all applicable filters for governance events
func (c *Cardano) filterGovernanceEvent(ge event.GovernanceEvent) bool {
	// Check DRep filter
	if c.filterSet.hasDRepFilter {
		if !c.matchDRepFilterGovernance(ge) {
			return false
		}
	}
	// Future: pool filter, address filter for governance
	return true
}

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
			hexStr := hex.EncodeToString(drepHash)
			if _, exists := c.filterSet.dreps.hexDRepIds[hexStr]; exists {
				return true
			}
		}
	}

	// Also check VotingProcedures from raw transaction if available
	if te.Transaction != nil {
		for voter := range te.Transaction.VotingProcedures() {
			if voter.Type == common.VoterTypeDRepKeyHash ||
				voter.Type == common.VoterTypeDRepScriptHash {
				hexStr := hex.EncodeToString(voter.Hash[:])
				if _, exists := c.filterSet.dreps.hexDRepIds[hexStr]; exists {
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

		// Compute hex string from certificate hash for O(1) lookup
		hexStr := hex.EncodeToString(poolKeyHash)

		// O(1) lookup: check if this hex maps to a filtered pool
		if _, exists := c.filterSet.pools.hexToBech32[hexStr]; exists {
			return true
		}

		// Also check direct hex match in hexPoolIds
		if _, exists := c.filterSet.pools.hexPoolIds[hexStr]; exists {
			return true
		}
	}
	return false
}

// Stop the cardano filter
func (c *Cardano) Stop() error {
	c.stopOnce.Do(func() {
		if c.doneChan != nil {
			close(c.doneChan)
		}
		// Wait for goroutine to exit before closing channels
		c.wg.Wait()
		if c.inputChan != nil {
			close(c.inputChan)
		}
		if c.outputChan != nil {
			close(c.outputChan)
		}
		if c.errorChan != nil {
			close(c.errorChan)
		}
	})
	return nil
}

// ErrorChan returns the plugin's error channel
func (c *Cardano) ErrorChan() <-chan error {
	return c.errorChan
}

// InputChan returns the input event channel
func (c *Cardano) InputChan() chan<- event.Event {
	return c.inputChan
}

// OutputChan returns the output event channel
func (c *Cardano) OutputChan() <-chan event.Event {
	return c.outputChan
}
