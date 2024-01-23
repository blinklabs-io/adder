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

package push

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/fcm"
	"github.com/blinklabs-io/snek/input/chainsync"
	"github.com/blinklabs-io/snek/internal/logging"
	"github.com/blinklabs-io/snek/plugin"
	"golang.org/x/oauth2/google"
)

type PushOutput struct {
	errorChan              chan error
	eventChan              chan event.Event
	logger                 plugin.Logger
	accessToken            string
	accessTokenUrl         string
	projectID              string
	serviceAccountFilePath string
	fcmTokens              []string
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

	if err := p.GetProjectId(); err != nil {
		logging.GetLogger().Fatalf("Failed to get project ID: %v", err)
	}
	return p
}

func (p *PushOutput) Start() error {
	logger := logging.GetLogger()
	logger.Infof("starting push notification server")
	go func() {
		for {
			evt, ok := <-p.eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			// Get access token per each event
			if err := p.GetAccessToken(); err != nil {
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
				fmt.Println("Snek")
				fmt.Printf(
					"New Block!\nBlockNumber: %d, SlotNumber: %d\nHash: %s",
					bc.BlockNumber,
					bc.SlotNumber,
					be.BlockHash,
				)

				// Create notification message
				title := "Snek"
				body := fmt.Sprintf(
					"New Block!\nBlockNumber: %d, SlotNumber: %d\nHash: %s",
					bc.BlockNumber,
					bc.SlotNumber,
					be.BlockHash,
				)

				// Send notification
				p.processFcmNotifications(title, body)

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
				context := evt.Context
				if context == nil {
					panic(fmt.Errorf("ERROR: %v", context))
				}

				te := payload.(chainsync.TransactionEvent)
				tc := context.(chainsync.TransactionContext)

				// Create notification message
				title := "Snek"

				// Get metadata
				var cip20Message string
				if te.Metadata != nil {
					jsonMessage, err := extractCIP20FromMetadata(te.Metadata)
					if err != nil {
						fmt.Println("Error:", err)
					} else {
						cip20Message = jsonMessage
						fmt.Println("JSON CIP20 Message:", cip20Message)
					}
				}

				var body string
				if cip20Message != "" {
					body = fmt.Sprintf(
						"New Transaction!\nBlockNumber: %d, SlotNumber: %d\nInputs: %d, Outputs: %d\nFee: %d\nHash: %s\nMetadata: %s",
						tc.BlockNumber,
						tc.SlotNumber,
						len(te.Inputs),
						len(te.Outputs),
						te.Fee,
						tc.TransactionHash,
						cip20Message,
					)
				} else {
					body = fmt.Sprintf(
						"New Transaction!\nBlockNumber: %d, SlotNumber: %d\nInputs: %d, Outputs: %d\nFee: %d\nHash: %s",
						tc.BlockNumber,
						tc.SlotNumber,
						len(te.Inputs),
						len(te.Outputs),
						te.Fee,
						tc.TransactionHash,
					)
				}
				// Send notification
				p.processFcmNotifications(title, body)

			default:
				fmt.Println("Snek")
				fmt.Printf("New Event!\nEvent: %v", evt)
			}
		}
	}()
	return nil
}

// refreshFcmTokens adds only the new FCM tokens to the fcmTokens slice
func (p *PushOutput) refreshFcmTokens() {
	tokenMap := GetFcmTokens()

	p.fcmTokens = p.fcmTokens[:0]
	for token := range tokenMap {
		p.fcmTokens = append(p.fcmTokens, token)
	}
}

func (p *PushOutput) processFcmNotifications(title, body string) {
	// Fetch new FCM tokens and add to p.fcmTokens
	p.refreshFcmTokens()

	// If no FCM tokens exist, log and exit
	if len(p.fcmTokens) == 0 {
		logging.GetLogger().
			Warnln("No FCM tokens found. Skipping notification.")
		return
	}

	// Send notification to each FCM token
	for _, fcmToken := range p.fcmTokens {
		msg := fcm.NewMessage(
			fcmToken,
			fcm.WithNotification(title, body),
		)

		if err := fcm.Send(p.accessToken, p.projectID, msg); err != nil {
			logging.GetLogger().
				Errorf("Failed to send message to token %s: %v", fcmToken, err)
			continue
		}
		logging.GetLogger().
			Infof("Message sent successfully to token %s!", fcmToken)
	}
}

func (p *PushOutput) GetAccessToken() error {
	data, err := os.ReadFile(p.serviceAccountFilePath)
	if err != nil {
		logging.GetLogger().
			Fatalf("Failed to read the credential file: %v", err)
		return err
	}

	conf, err := google.JWTConfigFromJSON(data, p.accessTokenUrl)
	if err != nil {
		logging.GetLogger().
			Fatalf("Failed to parse the credential file: %v", err)
		return err
	}

	token, err := conf.TokenSource(context.Background()).Token()
	if err != nil {
		logging.GetLogger().Fatalf("Failed to get token: %v", err)
		return err
	}

	fmt.Println(token.AccessToken)
	p.accessToken = token.AccessToken
	return nil
}

// Get project ID from file
func (p *PushOutput) GetProjectId() error {
	data, err := os.ReadFile(p.serviceAccountFilePath)
	if err != nil {
		logging.GetLogger().
			Fatalf("Failed to read the credential file: %v", err)
		return err
	}

	// Get project ID from file
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		logging.GetLogger().
			Fatalf("Failed to parse the credential file: %v", err)
		return err
	}
	p.projectID = v["project_id"].(string)

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

// This should probably go in gouroboros module
// extractCIP20FromMetadata extracts the CIP20 message from the transaction metadata
// and returns it as a JSON string.
func extractCIP20FromMetadata(metadata *cbor.Value) (string, error) {
	if metadata == nil {
		return "", fmt.Errorf("metadata is nil")
	}

	metadataMap, ok := metadata.Value().(map[any]any)
	if !ok {
		return "", fmt.Errorf("metadata value is not of the expected map type")
	}

	// Extract the nested value for key 674
	nestedValue, found := metadataMap[uint64(674)]
	if !found {
		return "", fmt.Errorf("key 674 not found in metadata")
	}

	// Assert the nested value is a map
	nestedMap, ok := nestedValue.(map[any]any)
	if !ok {
		return "", fmt.Errorf("nested value for key 674 is not a map")
	}

	msgValue, found := nestedMap["msg"]
	if !found {
		return "", fmt.Errorf("key 'msg' not found in nested metadata map")
	}

	msgStruct := map[string]any{
		"674": map[string]any{
			"msg": msgValue,
		},
	}

	jsonBytes, err := json.Marshal(msgStruct)
	if err != nil {
		return "", fmt.Errorf("error marshalling message to JSON: %v", err)
	}

	return string(jsonBytes), nil
}
