// Copyright 2023 Blink Labs, LLC.
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
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/input/chainsync"
)

type ChainSync struct {
	errorChan              chan error
	inputChan              chan event.Event
	outputChan             chan event.Event
	filterAddress          string
	filterPolicyId         string
	filterAssetFingerprint string
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
			case chainsync.TransactionEvent:
				// Check address filter
				if c.filterAddress != "" {
					foundMatch := false
					// TODO: extract and compare stake addresses when this is done
					// https://github.com/blinklabs-io/gouroboros/issues/302
					for _, output := range v.Outputs {
						if output.Address().String() == c.filterAddress {
							foundMatch = true
							break
						}
					}
					if !foundMatch {
						continue
					}
				}
				// Check policy ID filter
				if c.filterPolicyId != "" {
					foundMatch := false
					for _, output := range v.Outputs {
						if output.Assets() != nil {
							for _, policyId := range output.Assets().Policies() {
								if policyId.String() == c.filterPolicyId {
									foundMatch = true
									break
								}
							}
						}
						if foundMatch {
							break
						}
					}
					if !foundMatch {
						continue
					}
				}
				// Check asset fingerprint filter
				if c.filterAssetFingerprint != "" {
					foundMatch := false
					for _, output := range v.Outputs {
						if output.Assets() != nil {
							for _, policyId := range output.Assets().Policies() {
								for _, assetName := range output.Assets().Assets(policyId) {
									assetFp := ledger.NewAssetFingerprint(policyId.Bytes(), assetName)
									if assetFp.String() == c.filterAssetFingerprint {
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
					if !foundMatch {
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
