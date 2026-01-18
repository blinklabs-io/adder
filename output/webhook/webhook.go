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

package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/version"
	"github.com/blinklabs-io/adder/plugin"
)

const (
	mainnetNetworkMagic uint32 = 764824073
	previewNetworkMagic uint32 = 2
	preprodNetworkMagic uint32 = 1

	// Default retry configuration
	defaultMaxRetries     = 3
	defaultInitialBackoff = 1 * time.Second
	defaultMaxBackoff     = 30 * time.Second
	defaultBackoffFactor  = 2.0
)

type WebhookOutput struct {
	errorChan      chan error
	eventChan      chan event.Event
	doneChan       chan struct{}
	wg             sync.WaitGroup
	logger         plugin.Logger
	format         string
	url            string
	username       string
	password       string
	skipVerify     bool
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	backoffFactor  float64
}

func New(options ...WebhookOptionFunc) *WebhookOutput {
	w := &WebhookOutput{
		format:         "adder",
		url:            "http://localhost:3000",
		skipVerify:     false,
		maxRetries:     defaultMaxRetries,
		initialBackoff: defaultInitialBackoff,
		maxBackoff:     defaultMaxBackoff,
		backoffFactor:  defaultBackoffFactor,
	}
	for _, option := range options {
		option(w)
	}
	return w
}

// Start the webhook output
func (w *WebhookOutput) Start() error {
	// Guard against double-start: wait for existing goroutine to exit
	if w.doneChan != nil {
		close(w.doneChan)
		w.wg.Wait()
	}
	w.eventChan = make(chan event.Event, 10)
	w.errorChan = make(chan error)
	w.doneChan = make(chan struct{})
	logger := logging.GetLogger()
	logger.Info("starting webhook server")
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for {
			select {
			case <-w.doneChan:
				return
			case evt, ok := <-w.eventChan:
				// Channel has been closed, which means we're shutting down
				if !ok {
					return
				}
				payload := evt.Payload
				if payload == nil {
					panic(fmt.Errorf("ERROR: %v", payload))
				}
				context := evt.Context
				switch evt.Type {
				case "chainsync.block":
					if context == nil {
						panic(fmt.Errorf("ERROR: %v", context))
					}
					be := payload.(event.BlockEvent)
					bc := context.(event.BlockContext)
					evt.Payload = be
					evt.Context = bc
				case "chainsync.rollback":
					re := payload.(event.RollbackEvent)
					evt.Payload = re
				case "chainsync.transaction":
					te := payload.(event.TransactionEvent)
					evt.Payload = te
				default:
					logger.Error("unknown event type: " + evt.Type)
					return
				}
				// Send webhook with retry logic and exponential backoff
				w.sendWebhookWithRetry(&evt)
			}
		}
	}()
	return nil
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

func formatWebhook(e *event.Event, format string) []byte {
	var data []byte
	var err error
	switch format {
	case "discord":
		var dwe DiscordWebhookEvent
		var dme DiscordMessageEmbed
		dmes := make([]*DiscordMessageEmbed, 0, 1)
		var dmefs []*DiscordMessageEmbedField
		switch e.Type {
		case "chainsync.block":
			be := e.Payload.(event.BlockEvent)
			bc := e.Context.(event.BlockContext)
			dme.Title = "New Cardano Block"
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Block Number",
				Value: strconv.FormatUint(bc.BlockNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Slot Number",
				Value: strconv.FormatUint(bc.SlotNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Block Hash",
				Value: be.BlockHash,
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Issuer Vkey",
				Value: be.IssuerVkey,
			})
			baseURL := getBaseURL(bc.NetworkMagic)
			dme.URL = fmt.Sprintf("%s/block/%s", baseURL, be.BlockHash)
		case "chainsync.rollback":
			be := e.Payload.(event.RollbackEvent)
			dme.Title = "Cardano Rollback"
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Slot Number",
				Value: strconv.FormatUint(be.SlotNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Block Hash",
				Value: be.BlockHash,
			})
		case "chainsync.transaction":
			te := e.Payload.(event.TransactionEvent)
			tc := e.Context.(event.TransactionContext)
			dme.Title = "New Cardano Transaction"
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Block Number",
				Value: strconv.FormatUint(tc.BlockNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Slot Number",
				Value: strconv.FormatUint(tc.SlotNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Inputs",
				Value: strconv.Itoa(len(te.Inputs)),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Outputs",
				Value: strconv.Itoa(len(te.Outputs)),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Fee",
				Value: strconv.FormatUint(te.Fee, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Transaction Hash",
				Value: tc.TransactionHash,
			})
			baseURL := getBaseURL(tc.NetworkMagic)
			dme.URL = fmt.Sprintf("%s/tx/%s", baseURL, tc.TransactionHash)
		default:
			dwe.Content = fmt.Sprintf("%v", e.Payload)
		}
		dme.Fields = dmefs
		dmes = append(dmes, &dme)
		dwe.Embeds = dmes

		data, err = json.Marshal(dwe)
		if err != nil {
			return data
		}
	default:
		data, err = json.Marshal(e)
		if err != nil {
			return data
		}
	}
	return data
}

type DiscordWebhookEvent struct {
	Content string                 `json:"content,omitempty"`
	Embeds  []*DiscordMessageEmbed `json:"embeds,omitempty"`
}

type DiscordMessageEmbed struct {
	URL    string                      `json:"url,omitempty"`
	Title  string                      `json:"title,omitempty"`
	Fields []*DiscordMessageEmbedField `json:"fields,omitempty"`
}

type DiscordMessageEmbedField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func getBaseURL(networkMagic uint32) string {
	switch networkMagic {
	case mainnetNetworkMagic:
		return "https://cexplorer.io"
	case preprodNetworkMagic:
		return "https://preprod.cexplorer.io"
	case previewNetworkMagic:
		return "https://preview.cexplorer.io"
	default:
		return "https://cexplorer.io" // default to mainnet if unknown network
	}
}

func (w *WebhookOutput) SendWebhook(e *event.Event) error {
	logger := logging.GetLogger()
	logger.Info(fmt.Sprintf("sending event %s to %s", e.Type, w.url))
	data := formatWebhook(e, w.format)
	// Setup request
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		w.url,
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add(
		"User-Agent",
		"Adder/"+version.GetVersionString(),
	)

	// Setup authorization
	if w.username != "" && w.password != "" {
		req.Header.Add("Authorization", basicAuth(w.username, w.password))
	}
	// Setup custom transport to allow self-signed SSL
	defaultTransport := http.DefaultTransport.(*http.Transport)
	// #nosec G402
	customTransport := &http.Transport{
		Proxy:                 defaultTransport.Proxy,
		DialContext:           defaultTransport.DialContext,
		MaxIdleConns:          defaultTransport.MaxIdleConns,
		IdleConnTimeout:       defaultTransport.IdleConnTimeout,
		ExpectContinueTimeout: defaultTransport.ExpectContinueTimeout,
		TLSHandshakeTimeout:   defaultTransport.TLSHandshakeTimeout,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: w.skipVerify},
	}
	client := &http.Client{Transport: customTransport}
	// Send payload
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	if resp == nil {
		return fmt.Errorf("failed to send payload: %s", data)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	defer resp.Body.Close()

	logger.Info(
		fmt.Sprintf("sent: %s, payload: %s, body: %s, response: %s, status: %d",
			w.url,
			string(data),
			string(respBody),
			resp.Status,
			resp.StatusCode,
		),
	)
	return nil
}

// sendWebhookWithRetry wraps SendWebhook with retry logic and exponential backoff
func (w *WebhookOutput) sendWebhookWithRetry(e *event.Event) {
	logger := logging.GetLogger()
	var lastErr error
	backoff := w.initialBackoff

	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		if attempt > 0 {
			logger.Warn(
				fmt.Sprintf(
					"webhook delivery failed, retrying (attempt %d/%d) after %v",
					attempt,
					w.maxRetries,
					backoff,
				),
				"url", w.url,
				"event_type", e.Type,
				"error", lastErr,
			)
			time.Sleep(backoff)

			// Calculate next backoff with exponential increase
			backoff = time.Duration(float64(backoff) * w.backoffFactor)
			if backoff > w.maxBackoff {
				backoff = w.maxBackoff
			}
		}

		err := w.SendWebhook(e)
		if err == nil {
			if attempt > 0 {
				logger.Info(
					fmt.Sprintf("webhook delivery succeeded after %d retries", attempt),
					"url", w.url,
					"event_type", e.Type,
				)
			}
			return
		}
		lastErr = err
	}

	// All retries exhausted
	logger.Error(
		fmt.Sprintf(
			"webhook delivery failed after %d retries, giving up",
			w.maxRetries,
		),
		"url", w.url,
		"event_type", e.Type,
		"error", lastErr,
	)

	// Send error to error channel for monitoring (non-blocking)
	select {
	case w.errorChan <- fmt.Errorf(
		"webhook delivery to %s failed after %d retries: %w",
		w.url,
		w.maxRetries,
		lastErr,
	):
	default:
		// Error channel is full or closed, just log
		logger.Warn("could not send error to error channel (full or closed)")
	}
}

// Stop the webhook output
func (w *WebhookOutput) Stop() error {
	if w.doneChan != nil {
		close(w.doneChan)
		w.doneChan = nil
	}
	// Wait for goroutine to exit before closing channels
	w.wg.Wait()
	if w.eventChan != nil {
		close(w.eventChan)
		w.eventChan = nil
	}
	if w.errorChan != nil {
		close(w.errorChan)
		w.errorChan = nil
	}
	return nil
}

// ErrorChan returns the plugin's error channel
func (w *WebhookOutput) ErrorChan() <-chan error {
	return w.errorChan
}

// InputChan returns the input event channel
func (w *WebhookOutput) InputChan() chan<- event.Event {
	return w.eventChan
}

// OutputChan always returns nil
func (w *WebhookOutput) OutputChan() <-chan event.Event {
	return nil
}
