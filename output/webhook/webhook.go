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
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/explorer"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/version"
	"github.com/blinklabs-io/adder/plugin"
)

const (
	// Default retry configuration
	defaultMaxRetries     = 3
	defaultInitialBackoff = 1 * time.Second
	defaultMaxBackoff     = 30 * time.Second
	defaultBackoffFactor  = 2.0
)

type WebhookOutput struct {
	mu             sync.Mutex
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.eventChan != nil {
		return nil
	}
	// Guard against double-start: wait for existing goroutine to exit
	if w.doneChan != nil {
		close(w.doneChan)
		w.doneChan = nil
		w.wg.Wait()
	}
	w.eventChan = make(chan event.Event, 10)
	w.errorChan = make(chan error)
	w.doneChan = make(chan struct{})
	logger := logging.GetLogger()
	logger.Info("starting webhook server")
	w.wg.Add(1)
	// Pass the channels as arguments so the goroutine never reads the
	// shared struct fields, which Stop() may mutate concurrently.
	go func(doneChan <-chan struct{}, eventChan <-chan event.Event, errorChan chan<- error) {
		defer w.wg.Done()
		for {
			select {
			case <-doneChan:
				return
			case evt, ok := <-eventChan:
				// Channel has been closed, which means we're shutting down
				if !ok {
					return
				}
				payload := evt.Payload
				if payload == nil {
					w.reportError(
						logger,
						errorChan,
						fmt.Errorf(
							"received event with nil payload (type %q)",
							evt.Type,
						),
					)
					continue
				}
				context := evt.Context
				switch evt.Type {
				case event.TypeBlock:
					if context == nil {
						w.reportError(
							logger,
							errorChan,
							fmt.Errorf(
								"received %q event with nil context",
								evt.Type,
							),
						)
						continue
					}
					if _, ok := payload.(event.BlockEvent); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
					if _, ok := context.(event.BlockContext); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedContextErr(evt.Type, context),
						)
						continue
					}
				case event.TypeRollback:
					if _, ok := payload.(event.RollbackEvent); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
				case event.TypeTransaction:
					if _, ok := payload.(event.TransactionEvent); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
					if _, ok := context.(event.TransactionContext); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedContextErr(evt.Type, context),
						)
						continue
					}
				case event.TypeGovernance:
					if _, ok := payload.(event.GovernanceEvent); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
					if _, ok := context.(event.GovernanceContext); !ok {
						w.reportError(
							logger,
							errorChan,
							unexpectedContextErr(evt.Type, context),
						)
						continue
					}
				default:
					w.reportError(
						logger,
						errorChan,
						fmt.Errorf("received unknown event type %q", evt.Type),
					)
					continue
				}
				// Send webhook with retry logic and exponential backoff
				w.sendWebhookWithRetry(doneChan, errorChan, &evt)
			}
		}
	}(w.doneChan, w.eventChan, w.errorChan)
	return nil
}

// reportError surfaces an error on the plugin error channel without
// blocking. If no consumer is ready, the error is logged instead. This
// mirrors the non-blocking delivery used by sendWebhookWithRetry.
func (w *WebhookOutput) reportError(logger plugin.Logger, errorChan chan<- error, err error) {
	logger.Error(err.Error())
	select {
	case errorChan <- err:
	default:
		logger.Warn("could not send error to error channel (full)")
	}
}

// unexpectedPayloadErr builds an error describing a payload whose dynamic
// type does not match the one expected for the given event type.
func unexpectedPayloadErr(eventType string, payload any) error {
	return fmt.Errorf(
		"unexpected payload type %T for event %q",
		payload,
		eventType,
	)
}

// unexpectedContextErr builds an error describing a context whose dynamic
// type does not match the one expected for the given event type.
func unexpectedContextErr(eventType string, context any) error {
	return fmt.Errorf(
		"unexpected context type %T for event %q",
		context,
		eventType,
	)
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

func formatWebhook(e *event.Event, format string) ([]byte, error) {
	var data []byte
	var err error
	switch format {
	case "discord":
		var dwe DiscordWebhookEvent
		var dme DiscordMessageEmbed
		dmes := make([]*DiscordMessageEmbed, 0, 1)
		var dmefs []*DiscordMessageEmbedField
		switch e.Type {
		case event.TypeBlock:
			be, ok := e.Payload.(event.BlockEvent)
			if !ok {
				return nil, unexpectedPayloadErr(e.Type, e.Payload)
			}
			bc, ok := e.Context.(event.BlockContext)
			if !ok {
				return nil, unexpectedContextErr(e.Type, e.Context)
			}
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
		case event.TypeRollback:
			be, ok := e.Payload.(event.RollbackEvent)
			if !ok {
				return nil, unexpectedPayloadErr(e.Type, e.Payload)
			}
			dme.Title = "Cardano Rollback"
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Slot Number",
				Value: strconv.FormatUint(be.SlotNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Block Hash",
				Value: be.BlockHash,
			})
		case event.TypeTransaction:
			te, ok := e.Payload.(event.TransactionEvent)
			if !ok {
				return nil, unexpectedPayloadErr(e.Type, e.Payload)
			}
			tc, ok := e.Context.(event.TransactionContext)
			if !ok {
				return nil, unexpectedContextErr(e.Type, e.Context)
			}
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
		case event.TypeGovernance:
			ge, ok := e.Payload.(event.GovernanceEvent)
			if !ok {
				return nil, unexpectedPayloadErr(e.Type, e.Payload)
			}
			gc, ok := e.Context.(event.GovernanceContext)
			if !ok {
				return nil, unexpectedContextErr(e.Type, e.Context)
			}
			dme.Title = "Cardano Governance Event"
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Block Number",
				Value: strconv.FormatUint(gc.BlockNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Slot Number",
				Value: strconv.FormatUint(gc.SlotNumber, 10),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Proposals",
				Value: strconv.Itoa(len(ge.ProposalProcedures)),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Votes",
				Value: strconv.Itoa(len(ge.VotingProcedures)),
			})
			dmefs = append(dmefs, &DiscordMessageEmbedField{
				Name:  "Transaction Hash",
				Value: gc.TransactionHash,
			})
			baseURL := getBaseURL(gc.NetworkMagic)
			dme.URL = fmt.Sprintf("%s/tx/%s", baseURL, gc.TransactionHash)
		default:
			dwe.Content = fmt.Sprintf("%v", e.Payload)
		}
		dme.Fields = dmefs
		dmes = append(dmes, &dme)
		dwe.Embeds = dmes

		data, err = json.Marshal(dwe)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal webhook payload: %w", err)
		}
	default:
		data, err = json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal webhook payload: %w", err)
		}
	}
	return data, nil
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
	return explorer.BaseURL(networkMagic)
}

func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "invalid-url"
	}
	// Strip user info, path, raw query, and fragment to avoid credential/token leaks
	u.User = nil
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// log returns the plugin logger, or the global logger if unset.
func (w *WebhookOutput) log() plugin.Logger {
	if w.logger != nil {
		return w.logger
	}
	return logging.GetLoggerForComponent("output.webhook")
}

func (w *WebhookOutput) SendWebhook(e *event.Event) error {
	logger := w.log()
	logger.Info("sending event", "type", e.Type, "url", sanitizeURL(w.url))
	data, err := formatWebhook(e, w.format)
	if err != nil {
		return err
	}
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
	// #nosec G402
	customTransport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: w.skipVerify},
	}
	// Copy connection-pool tuning from the default transport when it is the
	// expected concrete type, rather than asserting unconditionally (which
	// would panic if DefaultTransport were replaced).
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		customTransport.Proxy = defaultTransport.Proxy
		customTransport.DialContext = defaultTransport.DialContext
		customTransport.MaxIdleConns = defaultTransport.MaxIdleConns
		customTransport.IdleConnTimeout = defaultTransport.IdleConnTimeout
		customTransport.ExpectContinueTimeout = defaultTransport.ExpectContinueTimeout
		customTransport.TLSHandshakeTimeout = defaultTransport.TLSHandshakeTimeout
	}
	client := &http.Client{Transport: customTransport}
	// Send payload
	// #nosec G704 -- Webhook URL is user-configured and intentionally allowed.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	if resp == nil {
		return fmt.Errorf("failed to send payload to %s", sanitizeURL(w.url))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned status: %d", resp.StatusCode)
	}

	logger.Info(
		"sent webhook",
		"url",
		sanitizeURL(w.url),
		"payload_size",
		len(data),
		"response_size",
		len(respBody),
		"status",
		resp.StatusCode,
	)

	logger.Debug(
		"sent webhook diagnostics",
		"url",
		sanitizeURL(w.url),
		"payload_size",
		len(data),
		"response_size",
		len(respBody),
		"status",
		resp.StatusCode,
	)
	return nil
}

// sendWebhookWithRetry wraps SendWebhook with retry logic and exponential backoff
func (w *WebhookOutput) sendWebhookWithRetry(doneChan <-chan struct{}, errorChan chan<- error, e *event.Event) {
	logger := w.log()
	var lastErr error
	backoff := w.initialBackoff

	logger.Debug("starting webhook delivery with retry", "url", sanitizeURL(w.url), "max_retries", w.maxRetries)

	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		if attempt > 0 {
			logger.Warn(
				"webhook delivery failed, retrying",
				"attempt",
				attempt,
				"max_retries",
				w.maxRetries,
				"delay",
				backoff,
				"url",
				sanitizeURL(w.url),
				"event_type",
				e.Type,
				"error",
				lastErr,
			)
			// Responsive sleep
			select {
			case <-doneChan:
				return
			case <-time.After(backoff):
			}

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
					"webhook delivery succeeded",
					"retries",
					attempt,
					"url",
					sanitizeURL(w.url),
					"event_type",
					e.Type,
				)
			}
			return
		}
		lastErr = err
	}

	// All retries exhausted
	logger.Error(
		"webhook delivery failed, giving up",
		"max_retries",
		w.maxRetries,
		"url",
		sanitizeURL(w.url),
		"event_type",
		e.Type,
		"error",
		lastErr,
	)

	// Send error to error channel for monitoring (non-blocking)
	select {
	case errorChan <- fmt.Errorf(
		"webhook delivery to %s failed after %d retries: %w",
		sanitizeURL(w.url),
		w.maxRetries,
		lastErr,
	):
	default:
		// Error channel is full, just log
		logger.Warn("could not send error to error channel (full)")
	}
}

// Stop the webhook output
func (w *WebhookOutput) Stop() error {
	w.mu.Lock()
	if w.doneChan != nil {
		close(w.doneChan)
		w.doneChan = nil
	}
	w.mu.Unlock()

	// Wait for goroutine to exit before closing channels
	w.wg.Wait()

	w.mu.Lock()
	if w.eventChan != nil {
		close(w.eventChan)
		w.eventChan = nil
	}
	if w.errorChan != nil {
		close(w.errorChan)
		w.errorChan = nil
	}
	w.mu.Unlock()
	return nil
}

// ErrorChan returns the plugin's error channel
func (w *WebhookOutput) ErrorChan() <-chan error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.errorChan
}

// InputChan returns the input event channel
func (w *WebhookOutput) InputChan() chan<- event.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.eventChan
}

// OutputChan always returns nil
func (w *WebhookOutput) OutputChan() <-chan event.Event {
	return nil
}
