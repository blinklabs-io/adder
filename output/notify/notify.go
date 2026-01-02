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

package notify

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/gen2brain/beeep"
)

//go:embed icon.png
var icon []byte

type NotifyOutput struct {
	errorChan chan error
	eventChan chan event.Event
	logger    plugin.Logger
	title     string
}

func New(options ...NotifyOptionFunc) *NotifyOutput {
	n := &NotifyOutput{
		title: "Adder",
	}
	for _, option := range options {
		option(n)
	}
	return n
}

// Start the notify output
func (n *NotifyOutput) Start() error {
	n.eventChan = make(chan event.Event, 10)
	n.errorChan = make(chan error)
	// Write our icon asset
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(fmt.Sprintf("%s/%s", userCacheDir, "adder")); os.IsNotExist(
		err,
	) {
		err = os.MkdirAll(
			fmt.Sprintf("%s/%s", userCacheDir, "adder"),
			os.ModePerm,
		)
		if err != nil {
			panic(err)
		}
	}
	filename := fmt.Sprintf("%s/%s/%s", userCacheDir, "adder", "icon.png")
	if err := os.WriteFile(filename, icon, 0o600); err != nil {
		panic(err)
	}
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

				be := payload.(event.BlockEvent)
				bc := context.(event.BlockContext)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf(
						"New Block!\nBlockNumber: %d, SlotNumber: %d, TransactionCount: %d\nHash: %s",
						bc.BlockNumber,
						bc.SlotNumber,
						be.TransactionCount,
						be.BlockHash,
					),
					filename,
				)
				if err != nil {
					panic(err)
				}
			case "chainsync.rollback":
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}

				re := payload.(event.RollbackEvent)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf("Rollback!\nSlotNumber: %d\nBlockHash: %s",
						re.SlotNumber,
						re.BlockHash,
					),
					filename,
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

				te := payload.(event.TransactionEvent)
				tc := context.(event.TransactionContext)
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
					filename,
				)
				if err != nil {
					panic(err)
				}
			default:
				err := beeep.Notify(
					n.title,
					fmt.Sprintf("New Event!\nEvent: %v", evt),
					filename,
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
	if n.eventChan != nil {
		close(n.eventChan)
		n.eventChan = nil
	}
	if n.errorChan != nil {
		close(n.errorChan)
		n.errorChan = nil
	}
	return nil
}

// ErrorChan returns the plugin's error channel
func (n *NotifyOutput) ErrorChan() <-chan error {
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
