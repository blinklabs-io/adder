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
	"context"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

type Cardano struct {
	plugin.Base
	filterSet filterSet
}

// New returns a new Cardano object with the specified options applied
func New(options ...CardanoOptionFunc) *Cardano {
	c := &Cardano{}
	for _, option := range options {
		option(c)
	}
	return c
}

// Role identifies this plugin as a pipeline filter.
func (c *Cardano) Role() plugin.PluginType { return plugin.PluginTypeFilter }

// Start the cardano filter
func (c *Cardano) Start() error {
	return c.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (c *Cardano) StartContext(ctx context.Context) error {
	return c.StartRun(ctx,
		plugin.BaseConfig{HasInput: true, HasOutput: true},
		c.start,
		plugin.ShutdownHooks{},
	)
}

func (c *Cardano) start(ctx context.Context) error {
	c.Go(c.processEvents)
	return nil
}

// processEvents handles incoming events and applies filters
func (c *Cardano) processEvents() {
	done, in := c.Done(), c.Input()
	for {
		select {
		case <-done:
			return
		case evt, ok := <-in:
			// Channel closed: we're shutting down
			if !ok {
				return
			}
			if c.filterEvent(evt) && !c.Emit(evt) {
				return
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
func (c *Cardano) filterDRepCertificateEvent(
	de event.DRepCertificateEvent,
) bool {
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
	return c.Shutdown(plugin.ShutdownHooks{})
}

var _ plugin.ManagedPlugin = (*Cardano)(nil)
