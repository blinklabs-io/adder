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

package notify

import (
	"fmt"

	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/input/chainsync"
	"github.com/blinklabs-io/snek/plugin"
	"github.com/gen2brain/beeep"
)

type NotifyOutput struct {
	errorChan chan error
	eventChan chan event.Event
	logger    plugin.Logger
	title     string
}

func New(options ...NotifyOptionFunc) *NotifyOutput {
	n := &NotifyOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
		title:     "Snek",
	}
	for _, option := range options {
		option(n)
	}
	return n
}

// Start the notify output
func (n *NotifyOutput) Start() error {
	go func() {
		for {
			evt, ok := <-n.eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			switch evt.Type {
			case "chainsync.block":
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}
				context := evt.Context
				if context == nil {
					panic(fmt.Errorf("ERROR: %v", context))
				}

				be := payload.(chainsync.BlockEvent)
				bc := context.(chainsync.BlockContext)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf(
						"New Block!\nBlockNumber: %d, SlotNumber: %d, TransactionCount: %d\nHash: %s",
						bc.BlockNumber,
						bc.SlotNumber,
						be.TransactionCount,
						be.BlockHash,
					),
					"assets/snek-icon.png",
				)
				if err != nil {
					panic(err)
				}
			case "chainsync.rollback":
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}

				re := payload.(chainsync.RollbackEvent)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf("Rollback!\nSlotNumber: %d\nBlockHash: %s",
						re.SlotNumber,
						re.BlockHash,
					),
					"assets/snek-icon.png",
				)
				if err != nil {
					panic(err)
				}
			case "chainsync.transaction":
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}
				context := evt.Context
				if context == nil {
					panic(fmt.Errorf("ERROR: %v", context))
				}

				te := payload.(chainsync.TransactionEvent)
				tc := context.(chainsync.TransactionContext)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf(
						"New Transaction!\nBlockNumber: %d, SlotNumber: %d\nInputs: %d, Outputs: %d\nFee: %d\nHash: %s",
						tc.BlockNumber,
						tc.SlotNumber,
						len(te.Inputs),
						len(te.Outputs),
						te.Fee,
						tc.TransactionHash,
					),
					"assets/snek-icon.png",
				)
				if err != nil {
					panic(err)
				}
			default:
				err := beeep.Notify(
					n.title,
					fmt.Sprintf("New Event!\nEvent: %v", evt),
					"assets/snek-icon.png",
				)
				if err != nil {
					panic(err)
				}
			}
		}
	}()
	return nil
}

// Stop the embedded output
func (n *NotifyOutput) Stop() error {
	close(n.eventChan)
	close(n.errorChan)
	return nil
}

// ErrorChan returns the input error channel
func (n *NotifyOutput) ErrorChan() chan error {
	return n.errorChan
}

// InputChan returns the input event channel
func (n *NotifyOutput) InputChan() chan<- event.Event {
	return n.eventChan
}

// OutputChan always returns nil
func (n *NotifyOutput) OutputChan() <-chan event.Event {
	return nil
}
