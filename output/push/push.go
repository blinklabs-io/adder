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

package push

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"sync"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/output/push/fcm"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"golang.org/x/oauth2/google"
)

type tokenProvider interface {
	GetToken() (string, error)
}

type googleTokenProvider struct {
	serviceAccountFilePath string
	accessTokenUrl         string
}

func (g *googleTokenProvider) GetToken() (string, error) {
	data, err := os.ReadFile(g.serviceAccountFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read credential file: %w", err)
	}

	conf, err := google.JWTConfigFromJSON(data, g.accessTokenUrl)
	if err != nil {
		return "", fmt.Errorf("failed to parse credential file: %w", err)
	}

	token, err := conf.TokenSource(context.Background()).Token()
	if err != nil {
		return "", fmt.Errorf("failed to get token: %w", err)
	}
	return token.AccessToken, nil
}

type PushOutput struct {
	mu                     sync.Mutex
	wg                     sync.WaitGroup
	errorChan              chan error
	eventChan              chan event.Event
	logger                 plugin.Logger
	accessToken            string
	accessTokenUrl         string
	projectID              string
	serviceAccountFilePath string
	fcmTokens              []string
	tokenProvider          tokenProvider
}

type Notification struct {
	Message  string   `json:"message"`
	Tokens   []string `json:"tokens"`
	Platform int      `json:"platform"`
}

type PushPayload struct {
	Notifications []Notification `json:"notifications"`
}

func New(options ...PushOptionFunc) (*PushOutput, error) {
	p := &PushOutput{}
	for _, option := range options {
		option(p)
	}

	if p.serviceAccountFilePath == "" {
		return nil, errors.New("service account file path is required")
	}

	if p.tokenProvider == nil {
		p.tokenProvider = &googleTokenProvider{
			serviceAccountFilePath: p.serviceAccountFilePath,
			accessTokenUrl:         p.accessTokenUrl,
		}
	}

	if err := p.GetProjectId(); err != nil {
		return nil, fmt.Errorf("failed to get project ID: %w", err)
	}
	return p, nil
}

// log returns the plugin logger, or the global logger if unset.
func (p *PushOutput) log() plugin.Logger {
	if p.logger != nil {
		return p.logger
	}
	return logging.GetLoggerForComponent("output.push")
}

func (p *PushOutput) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.eventChan != nil {
		return nil
	}
	p.eventChan = make(chan event.Event, 10)
	p.errorChan = make(chan error, 16)
	logger := p.log()
	logger.Info("starting push notification server")
	eventChan := p.eventChan
	errorChan := p.errorChan
	p.wg.Add(1)
	go func(eventChan <-chan event.Event, errorChan chan<- error) {
		defer p.wg.Done()
		for {
			evt, ok := <-eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			// Get access token per each event
			if err := p.GetAccessToken(); err != nil {
				err = fmt.Errorf("failed to get access token: %w", err)
				slog.Error(err.Error())
				select {
				case errorChan <- err:
				default:
				}
				continue
			}

			switch evt.Type {
			case event.TypeBlock:
				payload := evt.Payload
				if payload == nil {
					slog.Error("block event has nil payload")
					continue
				}
				context := evt.Context
				if context == nil {
					slog.Error("block event has nil context")
					continue
				}

				be := payload.(event.BlockEvent)
				bc := context.(event.BlockContext)
				logger.Debug(
					"New Block!",
					"block_number",
					bc.BlockNumber,
					"slot_number",
					bc.SlotNumber,
					"hash",
					be.BlockHash,
				)

				// Create notification message
				title := "Adder"
				body := fmt.Sprintf(
					"New Block!\nBlockNumber: %d, SlotNumber: %d\nHash: %s",
					bc.BlockNumber,
					bc.SlotNumber,
					be.BlockHash,
				)

				// Send notification
				p.processFcmNotifications(errorChan, title, body)

			case event.TypeRollback:
				payload := evt.Payload
				if payload == nil {
					slog.Error("rollback event has nil payload")
					continue
				}

				re := payload.(event.RollbackEvent)
				logger.Debug(
					"Rollback!",
					"slot_number",
					re.SlotNumber,
					"block_hash",
					re.BlockHash,
				)
			case event.TypeTransaction:
				payload := evt.Payload
				if payload == nil {
					slog.Error("transaction event has nil payload")
					continue
				}
				context := evt.Context
				if context == nil {
					slog.Error("transaction event has nil context")
					continue
				}

				te := payload.(event.TransactionEvent)
				tc := context.(event.TransactionContext)

				// Create notification message
				title := "Adder"

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
				p.processFcmNotifications(errorChan, title, body)

			default:
				fmt.Println("Adder")
				fmt.Printf("New Event!\nEvent: %v", evt)
			}
		}
	}(eventChan, errorChan)
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

func truncToken(token string) string {
	if len(token) <= 16 {
		return "REDACTED"
	}
	return token[:8] + "..." + token[len(token)-8:]
}

func (p *PushOutput) processFcmNotifications(errorChan chan<- error, title, body string) {
	logger := p.log()
	// Fetch new FCM tokens and add to p.fcmTokens
	p.refreshFcmTokens()

	// If no FCM tokens exist, log and return
	if len(p.fcmTokens) == 0 {
		logger.Info("No FCM tokens found. Skipping notification.")
		return
	}

	// Send notification to each FCM token
	for _, fcmToken := range p.fcmTokens {
		msg, err := fcm.NewMessage(
			fcmToken,
			fcm.WithNotification(title, body),
		)
		if err != nil {
			logger.Error(
				"Failed to create message for token",
				"token",
				truncToken(fcmToken),
				"error",
				err,
			)
			select {
			case errorChan <- fmt.Errorf("failed to create message for token %s: %w", truncToken(fcmToken), err):
			default:
			}
			continue
		}

		if err := fcm.Send(p.accessToken, p.projectID, msg); err != nil {
			logger.Error(
				"Failed to send message to token",
				"token",
				truncToken(fcmToken),
				"error",
				err,
			)
			select {
			case errorChan <- fmt.Errorf("failed to send message to token %s: %w", truncToken(fcmToken), err):
			default:
			}
			continue
		}
		logger.Info(
			"Message sent successfully to token",
			"token",
			truncToken(fcmToken),
		)
	}
}

func (p *PushOutput) GetAccessToken() error {
	token, err := p.tokenProvider.GetToken()
	if err != nil {
		return err
	}
	p.accessToken = token
	return nil
}

// GetProjectId gets project ID from file
func (p *PushOutput) GetProjectId() error {
	if p.serviceAccountFilePath == "" {
		return errors.New("service account file path is empty")
	}
	data, err := os.ReadFile(p.serviceAccountFilePath)
	if err != nil {
		return fmt.Errorf("failed to read credential file: %w", err)
	}

	// Get project ID from file
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("failed to parse credential file: %w", err)
	}
	projectID, ok := v["project_id"].(string)
	if !ok || projectID == "" {
		return errors.New("invalid or empty project_id in service account file")
	}
	p.projectID = projectID

	return nil
}

// Stop the embedded output
func (p *PushOutput) Stop() error {
	p.mu.Lock()
	if p.eventChan != nil {
		close(p.eventChan)
		p.eventChan = nil
	}
	p.mu.Unlock()

	p.wg.Wait()

	p.mu.Lock()
	if p.errorChan != nil {
		close(p.errorChan)
		p.errorChan = nil
	}
	p.mu.Unlock()
	return nil
}

// ErrorChan returns the plugin's error channel
func (p *PushOutput) ErrorChan() <-chan error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errorChan
}

// InputChan returns the input event channel
func (p *PushOutput) InputChan() chan<- event.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.eventChan
}

// OutputChan always returns nil
func (p *PushOutput) OutputChan() <-chan event.Event {
	return nil
}

// This should probably go in gouroboros module
// extractCIP20FromMetadata extracts the CIP20 message from the transaction metadata
// and returns it as a JSON string.
func extractCIP20FromMetadata(
	metadata common.TransactionMetadatum,
) (string, error) {
	if metadata == nil {
		return "", errors.New("metadata is nil")
	}

	metaMap, ok := metadata.(common.MetaMap)
	if !ok {
		return "", errors.New("metadata is not a MetaMap")
	}

	var nestedValue common.TransactionMetadatum
	found := false
	for _, pair := range metaMap.Pairs {
		if keyInt, ok := pair.Key.(common.MetaInt); ok &&
			keyInt.Value.Cmp(big.NewInt(674)) == 0 {
			nestedValue = pair.Value
			found = true
			break
		}
	}
	if !found {
		return "", errors.New("key 674 not found in metadata")
	}

	nestedMap, ok := nestedValue.(common.MetaMap)
	if !ok {
		return "", errors.New("nested value for key 674 is not a map")
	}

	var msgValue common.TransactionMetadatum
	found = false
	for _, pair := range nestedMap.Pairs {
		if keyText, ok := pair.Key.(common.MetaText); ok &&
			keyText.Value == "msg" {
			msgValue = pair.Value
			found = true
			break
		}
	}
	if !found {
		return "", errors.New("key 'msg' not found in nested metadata map")
	}

	msgStruct := map[string]any{
		"674": map[string]any{
			"msg": metadatumToAny(msgValue),
		},
	}

	jsonBytes, err := json.Marshal(msgStruct)
	if err != nil {
		return "", fmt.Errorf("error marshalling message to JSON: %w", err)
	}

	return string(jsonBytes), nil
}

func keyToString(md common.TransactionMetadatum) string {
	switch v := md.(type) {
	case common.MetaInt:
		return v.Value.String()
	case common.MetaBytes:
		return hex.EncodeToString(v.Value)
	case common.MetaText:
		return v.Value
	default:
		return fmt.Sprintf("%v", metadatumToAny(md))
	}
}

func metadatumToAny(md common.TransactionMetadatum) any {
	switch v := md.(type) {
	case common.MetaInt:
		return v.Value
	case common.MetaBytes:
		return v.Value
	case common.MetaText:
		return v.Value
	case common.MetaList:
		var list []any
		for _, item := range v.Items {
			list = append(list, metadatumToAny(item))
		}
		return list
	case common.MetaMap:
		m := make(map[string]any)
		for _, pair := range v.Pairs {
			keyStr := keyToString(pair.Key)
			value := metadatumToAny(pair.Value)
			m[keyStr] = value
		}
		return m
	default:
		return nil
	}
}
