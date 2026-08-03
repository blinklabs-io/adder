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
	"sync"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
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
	case event.DRepCertificateEvent:
		return c.filterDRepCertificateEvent(v)
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

	// Pool and DRep IDs identify independent actors. When both are configured,
	// pass transactions involving either followed identity.
	if c.filterSet.hasPoolFilter && c.filterSet.hasDRepFilter {
		if !c.matchPoolFilterTx(te) && !c.matchDRepFilterTx(te) {
			return false
		}
	} else if c.filterSet.hasPoolFilter {
		if !c.matchPoolFilterTx(te) {
			return false
		}
	} else if c.filterSet.hasDRepFilter {
		if !c.matchDRepFilterTx(te) {
			return false
		}
	}

	return true
}

// filterDRepCertificateEvent checks DRep filter for DRep certificate events
func (c *Cardano) filterDRepCertificateEvent(de event.DRepCertificateEvent) bool {
	if !c.filterSet.hasDRepFilter {
		return true
	}

	if _, exists := c.filterSet.dreps.hexDRepIds[de.Certificate.DRepHash]; exists {
		return true
	}

	if _, exists := c.filterSet.dreps.bech32DRepIds[de.Certificate.DRepId]; exists {
		return true
	}

	return false
}

// filterGovernanceEvent checks all applicable filters for governance events
func (c *Cardano) filterGovernanceEvent(ge event.GovernanceEvent) bool {
	// Check address filter
	if c.filterSet.hasAddressFilter {
		if !c.matchAddressFilterGovernance(ge) {
			return false
		}
	}

	// Pool and DRep IDs identify independent actors. When both are configured,
	// pass governance events involving either followed identity.
	if c.filterSet.hasPoolFilter && c.filterSet.hasDRepFilter {
		if !c.matchPoolFilterGovernance(ge) &&
			!c.matchDRepFilterGovernance(ge) {
			return false
		}
	} else if c.filterSet.hasPoolFilter {
		if !c.matchPoolFilterGovernance(ge) {
			return false
		}
	} else if c.filterSet.hasDRepFilter {
		if !c.matchDRepFilterGovernance(ge) {
			return false
		}
	}

	return true
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
