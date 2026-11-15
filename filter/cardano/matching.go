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

import "github.com/blinklabs-io/adder/event"

// filterEvent returns true if the event should be passed through
func (c *filterSet) filterEvent(evt event.Event) bool {
	switch v := evt.Payload.(type) {
	case event.BlockEvent:
		return c.filterBlockEvent(v)
	case event.TransactionEvent:
		return c.filterTransactionEvent(v)
	case event.GovernanceEvent:
		return c.filterGovernanceEvent(v)
	case event.DRepCertificateEvent:
		return c.filterDRepCertificateEvent(v)
	default:
		// Pass through events we don't filter
		return true
	}
}

// filterBlockEvent checks pool filter for block events using O(1) lookup
func (c *filterSet) filterBlockEvent(be event.BlockEvent) bool {
	if !c.hasPoolFilter {
		return true
	}

	// O(1) lookup using pre-computed hexToBech32 map
	// Check if the issuer vkey (hex) maps to a filtered pool
	if _, exists := c.pools.hexToBech32[be.IssuerVkey]; exists {
		return true
	}

	// Also check direct hex match in hexPoolIds
	if _, exists := c.pools.hexPoolIds[be.IssuerVkey]; exists {
		return true
	}

	// Also check direct match in bech32PoolIds for bech32 format pool IDs
	if _, exists := c.pools.bech32PoolIds[be.IssuerVkey]; exists {
		return true
	}

	return false
}

// filterTransactionEvent checks all applicable filters with early exit on match
func (c *filterSet) filterTransactionEvent(te event.TransactionEvent) bool {
	// Check address filter
	if c.hasAddressFilter {
		if !c.matchAddressFilter(te) {
			return false
		}
	}

	// Check policy ID filter
	if c.hasPolicyFilter {
		if !c.matchPolicyFilter(te) {
			return false
		}
	}

	// Check asset fingerprint filter
	if c.hasAssetFilter {
		if !c.matchAssetFilter(te) {
			return false
		}
	}

	// Pool and DRep IDs identify independent actors. When both are configured,
	// pass transactions involving either followed identity.
	if c.hasPoolFilter && c.hasDRepFilter {
		if !c.matchPoolFilterTx(te) && !c.matchDRepFilterTx(te) {
			return false
		}
	} else if c.hasPoolFilter {
		if !c.matchPoolFilterTx(te) {
			return false
		}
	} else if c.hasDRepFilter {
		if !c.matchDRepFilterTx(te) {
			return false
		}
	}

	return true
}

// filterDRepCertificateEvent checks DRep filter for DRep certificate events
func (c *filterSet) filterDRepCertificateEvent(
	de event.DRepCertificateEvent,
) bool {
	if !c.hasDRepFilter {
		return true
	}

	if _, exists := c.dreps.hexDRepIds[de.Certificate.DRepHash]; exists {
		return true
	}

	if _, exists := c.dreps.bech32DRepIds[de.Certificate.DRepId]; exists {
		return true
	}

	return false
}

// filterGovernanceEvent checks all applicable filters for governance events
func (c *filterSet) filterGovernanceEvent(ge event.GovernanceEvent) bool {
	// Check address filter
	if c.hasAddressFilter {
		if !c.matchAddressFilterGovernance(ge) {
			return false
		}
	}

	// Pool and DRep IDs identify independent actors. When both are configured,
	// pass governance events involving either followed identity.
	if c.hasPoolFilter && c.hasDRepFilter {
		if !c.matchPoolFilterGovernance(ge) &&
			!c.matchDRepFilterGovernance(ge) {
			return false
		}
	} else if c.hasPoolFilter {
		if !c.matchPoolFilterGovernance(ge) {
			return false
		}
	} else if c.hasDRepFilter {
		if !c.matchDRepFilterGovernance(ge) {
			return false
		}
	}

	return true
}
