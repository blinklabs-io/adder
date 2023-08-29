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

package push

import (
	"fmt"
	"log"

	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/fcm"
	"github.com/blinklabs-io/snek/input/chainsync"
)

type PushOutput struct {
	errorChan chan error
	eventChan chan event.Event
}

type Notification struct {
	Tokens   []string `json:"tokens"`
	Platform int      `json:"platform"`
	Message  string   `json:"message"`
}

type PushPayload struct {
	Notifications []Notification `json:"notifications"`
}

func New(options ...PushOptionFunc) *PushOutput {
	p := &PushOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
	}
	for _, option := range options {
		option(p)
	}
	return p
}

func (p *PushOutput) Start() error {
	go func() {
		for {
			evt, ok := <-p.eventChan
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

				be := payload.(chainsync.BlockEvent)
				fmt.Println("Snek")
				fmt.Printf("New Block!\nBlockNumber: %d, SlotNumber: %d\nHash: %s",
					be.BlockNumber,
					be.SlotNumber,
					be.BlockHash,
				)
			case "chainsync.rollback":
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}

				re := payload.(chainsync.RollbackEvent)
				fmt.Println("Snek")
				fmt.Printf("Rollback!\nSlotNumber: %d\nBlockHash: %s",
					re.SlotNumber,
					re.BlockHash,
				)
			case "chainsync.transaction":
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}

				te := payload.(chainsync.TransactionEvent)
				accessToken := fcm.GetAccessToken()
				// TODO define where tokens will be fetched from or we add this to topic
				fcmToken := ""
				title := "Snek"
				body := fmt.Sprintf("New Transaction!\nBlockNumber: %d, SlotNumber: %d\nInputs: %d, Outputs: %d\nHash: %s",
					te.BlockNumber,
					te.SlotNumber,
					len(te.Inputs),
					len(te.Outputs),
					te.TransactionHash,
				)
				msg := fcm.NewMessage(
					fcmToken,
					fcm.WithNotification(title, body),
				)
				err := fcm.Send(accessToken, msg)
				if err != nil {
					log.Fatalf("Failed to send message: %v", err)
				}
				fmt.Println("Message 1 sent successfully!")

			default:
				fmt.Println("Snek")
				fmt.Printf("New Event!\nEvent: %v", evt)
			}
		}
	}()
	return nil
}

// Stop the embedded output
func (p *PushOutput) Stop() error {
	close(p.eventChan)
	close(p.errorChan)
	return nil
}

// ErrorChan returns the input error channel
func (p *PushOutput) ErrorChan() chan error {
	return p.errorChan
}

// InputChan returns the input event channel
func (p *PushOutput) InputChan() chan<- event.Event {
	return p.eventChan
}

// OutputChan always returns nil
func (p *PushOutput) OutputChan() <-chan event.Event {
	return nil
}
