// Copyright 2023 Blink Labs Software
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

package chainsync

import (
	"encoding/hex"
	"strings"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/input/chainsync"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/gouroboros/bech32"
	"github.com/blinklabs-io/gouroboros/ledger"
)

type ChainSync struct {
	errorChan               chan error
	inputChan               chan event.Event
	outputChan              chan event.Event
	logger                  plugin.Logger
	filterAddresses         []string
	filterAssetFingerprints []string
	filterPolicyIds         []string
	filterPoolIds           []string
}

// New returns a new ChainSync object with the specified options applied
func New(options ...ChainSyncOptionFunc) *ChainSync {
	c := &ChainSync{
		errorChan:  make(chan error),
		inputChan:  make(chan event.Event, 10),
		outputChan: make(chan event.Event, 10),
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Start the chain sync filter
func (c *ChainSync) Start() error {
	go func() {
		// TODO: pre-process filter params to be more useful for direct comparison
		for {
			evt, ok := <-c.inputChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			switch v := evt.Payload.(type) {
			case chainsync.BlockEvent:
				// Check pool filter
				if len(c.filterPoolIds) > 0 {
					filterMatched := false
					for _, filterPoolId := range c.filterPoolIds {
						isPoolBech32 := strings.HasPrefix(filterPoolId, "pool")
						foundMatch := false
						be := evt.Payload.(chainsync.BlockEvent)
						if be.IssuerVkey == filterPoolId {
							foundMatch = true
						} else if isPoolBech32 {
							issuerBytes, err := hex.DecodeString(be.IssuerVkey)
							if err != nil {
								// eat this error... nom nom nom
								continue
							}
							// lifted from gouroboros/ledger
							convData, err := bech32.ConvertBits(issuerBytes, 8, 5, true)
							if err != nil {
								continue
							}
							encoded, err := bech32.Encode("pool", convData)
							if err != nil {
								continue
							}
							if encoded == filterPoolId {
								foundMatch = true
							}
						}
						if foundMatch {
							filterMatched = true
							break
						}
					}
					// Skip the event if none of the filter values matched
					if !filterMatched {
						continue
					}
				}
			case chainsync.TransactionEvent:
				// Check address filter
				if len(c.filterAddresses) > 0 {
					filterMatched := false
					for _, filterAddress := range c.filterAddresses {
						isStakeAddress := strings.HasPrefix(filterAddress, "stake")
						foundMatch := false
						for _, output := range v.Outputs {
							if output.Address().String() == filterAddress {
								foundMatch = true
								break
							}
							if isStakeAddress {
								stakeAddr := output.Address().StakeAddress()
								if stakeAddr == nil {
									continue
								}
								if stakeAddr.String() == filterAddress {
									foundMatch = true
									break
								}
							}
						}
						if foundMatch {
							filterMatched = true
							break
						}
					}
					// Skip the event if none of the filter values matched
					if !filterMatched {
						continue
					}
				}
				// Check policy ID filter
				if len(c.filterPolicyIds) > 0 {
					filterMatched := false
					for _, filterPolicyId := range c.filterPolicyIds {
						foundMatch := false
						for _, output := range v.Outputs {
							if output.Assets() != nil {
								for _, policyId := range output.Assets().Policies() {
									if policyId.String() == filterPolicyId {
										foundMatch = true
										break
									}
								}
							}
							if foundMatch {
								break
							}
						}
						if foundMatch {
							filterMatched = true
							break
						}
					}
					// Skip the event if none of the filter values matched
					if !filterMatched {
						continue
					}
				}
				// Check asset fingerprint filter
				if len(c.filterAssetFingerprints) > 0 {
					filterMatched := false
					for _, filterAssetFingerprint := range c.filterAssetFingerprints {
						foundMatch := false
						for _, output := range v.Outputs {
							if output.Assets() != nil {
								for _, policyId := range output.Assets().Policies() {
									for _, assetName := range output.Assets().Assets(policyId) {
										assetFp := ledger.NewAssetFingerprint(policyId.Bytes(), assetName)
										if assetFp.String() == filterAssetFingerprint {
											foundMatch = true
										}
									}
									if foundMatch {
										break
									}
								}
								if foundMatch {
									break
								}
							}
						}
						if foundMatch {
							filterMatched = true
							break
						}
					}
					// Skip the event if none of the filter values matched
					if !filterMatched {
						continue
					}
				}
			}
			c.outputChan <- evt
		}
	}()
	return nil
}

// Stop the chain sync filter
func (c *ChainSync) Stop() error {
	close(c.inputChan)
	close(c.outputChan)
	close(c.errorChan)
	return nil
}

// ErrorChan returns the filter error channel
func (c *ChainSync) ErrorChan() chan error {
	return c.errorChan
}

// InputChan returns the input event channel
func (c *ChainSync) InputChan() chan<- event.Event {
	return c.inputChan
}

// OutputChan returns the output event channel
func (c *ChainSync) OutputChan() <-chan event.Event {
	return c.outputChan
}
