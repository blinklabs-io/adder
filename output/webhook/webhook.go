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
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	plugin.Base
	format          string
	url             string
	username        string
	password        string
	skipVerify      bool
	maxRetries      int
	initialBackoff  time.Duration
	maxBackoff      time.Duration
	backoffFactor   float64
	metricsObserver plugin.DeliveryMetricsObserver
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

// Role identifies this plugin as a pipeline output.
func (w *WebhookOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start the webhook output
func (w *WebhookOutput) Start() error {
	return w.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (w *WebhookOutput) StartContext(ctx context.Context) error {
	return w.StartRun(ctx,
		plugin.BaseConfig{HasInput: true},
		w.start,
		plugin.ShutdownHooks{},
	)
}

func (w *WebhookOutput) start(ctx context.Context) error {
	logger := logging.GetLogger()
	logger.Info("starting webhook server")
	doneChan, eventChan := w.Done(), w.Input()
	w.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
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
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
					if _, ok := context.(event.BlockContext); !ok {
						w.reportError(
							logger,
							unexpectedContextErr(evt.Type, context),
						)
						continue
					}
				case event.TypeRollback:
					if _, ok := payload.(event.RollbackEvent); !ok {
						w.reportError(
							logger,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
				case event.TypeTransaction:
					if _, ok := payload.(event.TransactionEvent); !ok {
						w.reportError(
							logger,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
					if _, ok := context.(event.TransactionContext); !ok {
						w.reportError(
							logger,
							unexpectedContextErr(evt.Type, context),
						)
						continue
					}
				case event.TypeGovernance:
					if _, ok := payload.(event.GovernanceEvent); !ok {
						w.reportError(
							logger,
							unexpectedPayloadErr(evt.Type, payload),
						)
						continue
					}
					if _, ok := context.(event.GovernanceContext); !ok {
						w.reportError(
							logger,
							unexpectedContextErr(evt.Type, context),
						)
						continue
					}
				default:
					w.reportError(
						logger,
						fmt.Errorf("received unknown event type %q", evt.Type),
					)
					continue
				}
				// Send webhook with retry logic and exponential backoff
				w.sendWebhookWithRetry(ctx, doneChan, &evt)
			}
		}
	})
	return nil
}

// reportError surfaces an error on the plugin error channel without
// blocking. If no consumer is ready, the error is logged instead.
func (w *WebhookOutput) reportError(logger plugin.Logger, err error) {
	w.recordOutcome("terminal")
	logger.Error(err.Error())
	if !w.TrySendError(err) {
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

// FormatDiscord formats an event as a Discord message using the existing webhook layout.
func FormatDiscord(e *event.Event) ([]byte, error) {
	return formatWebhook(e, "discord")
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
	if logger := w.Logger(); logger != nil {
		return logger
	}
	return logging.GetLoggerForComponent("output.webhook")
}

func (w *WebhookOutput) SendWebhook(e *event.Event) error {
	result := w.sendWebhook(context.Background(), e)
	if result.err == nil {
		w.recordOutcome("success")
	} else {
		w.recordOutcome("terminal")
	}
	return result.err
}

// SetDeliveryMetrics connects this output to its owning pipeline's aggregate metrics.
// Call before Start or SendWebhook.
func (w *WebhookOutput) SetDeliveryMetrics(
	observer plugin.DeliveryMetricsObserver,
) {
	w.metricsObserver = observer
}

func (w *WebhookOutput) recordOutcome(outcome string) {
	if w.metricsObserver != nil {
		w.metricsObserver.RecordWebhookOutcome(outcome)
	}
}

type deliveryAttempt struct {
	err        error
	retryable  bool
	retryAfter string
}

func (w *WebhookOutput) sendWebhook(
	ctx context.Context,
	e *event.Event,
) deliveryAttempt {
	logger := w.log()
	logger.Info("sending event", "type", e.Type, "url", sanitizeURL(w.url))
	data, err := formatWebhook(e, w.format)
	if err != nil {
		return deliveryAttempt{err: err}
	}
	parsedURL, err := url.Parse(w.url)
	if err != nil ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") ||
		parsedURL.Host == "" {
		return deliveryAttempt{err: errors.New("invalid webhook URL")}
	}
	// Setup request
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		w.url,
		bytes.NewReader(data),
	)
	if err != nil {
		return deliveryAttempt{err: err}
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
	// Preserve the HTTP client's existing redirect behavior, including its
	// default limit of 10 redirects and POST-to-GET conversion for 301/302/303.
	client := &http.Client{Transport: customTransport}
	defer customTransport.CloseIdleConnections()
	// Send payload
	// #nosec G704 -- Webhook URL is user-configured and intentionally allowed.
	started := time.Now()
	resp, err := client.Do(req)
	statusClass := "network"
	if resp != nil {
		statusClass = httpStatusClass(resp.StatusCode)
	}
	defer func() {
		elapsed := time.Since(started)
		if w.metricsObserver != nil {
			w.metricsObserver.RecordWebhookAttempt(statusClass, elapsed)
		}
	}()
	if err != nil {
		return deliveryAttempt{
			err:       err,
			retryable: !permanentTransportError(err),
		}
	}
	if resp == nil {
		return deliveryAttempt{
			err: fmt.Errorf(
				"failed to send payload to %s",
				sanitizeURL(w.url),
			),
			retryable: true,
		}
	}
	defer resp.Body.Close()

	// Response content is diagnostic only; cap work and avoid retaining it.
	responseSize, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		return deliveryAttempt{
			err: fmt.Errorf("server returned status: %d", status),
			retryable: status == http.StatusRequestTimeout ||
				status == http.StatusTooManyRequests ||
				status >= 500,
			retryAfter: resp.Header.Get("Retry-After"),
		}
	}

	logger.Info(
		"sent webhook",
		"url",
		sanitizeURL(w.url),
		"payload_size",
		len(data),
		"response_bytes_read",
		responseSize,
		"status",
		resp.StatusCode,
	)

	logger.Debug(
		"sent webhook diagnostics",
		"url",
		sanitizeURL(w.url),
		"payload_size",
		len(data),
		"response_bytes_read",
		responseSize,
		"status",
		resp.StatusCode,
	)
	return deliveryAttempt{}
}

func permanentTransportError(err error) bool {
	var authority x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	var hostname x509.HostnameError
	var roots x509.SystemRootsError
	var recordHeader tls.RecordHeaderError
	return errors.As(err, &authority) || errors.As(err, &certificate) ||
		errors.As(
			err,
			&hostname,
		) || errors.As(err, &roots) || errors.As(err, &recordHeader)
}

func retryAfterDelay(
	value string,
	now time.Time,
	bound time.Duration,
) (time.Duration, bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil &&
		seconds >= 0 {
		if seconds > int64(bound/time.Second) {
			return 0, true, true
		}
		return time.Duration(seconds) * time.Second, true, false
	} else if errors.Is(err, strconv.ErrRange) && strings.Trim(value, "0123456789") == "" {
		return 0, true, true
	}
	if date, err := http.ParseTime(value); err == nil {
		delay := date.Sub(now)
		if delay < 0 {
			return 0, false, false
		}
		return delay, true, delay > bound
	}
	return 0, false, false
}

// sendWebhookWithRetry wraps SendWebhook with retry logic and exponential backoff
func (w *WebhookOutput) sendWebhookWithRetry(
	ctx context.Context,
	doneChan <-chan struct{},
	e *event.Event,
) {
	logger := w.log()
	backoff := w.initialBackoff

	logger.Debug(
		"starting webhook delivery with retry",
		"url",
		sanitizeURL(w.url),
		"max_retries",
		w.maxRetries,
	)

	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		result := w.sendWebhook(ctx, e)
		if ctx.Err() != nil {
			return
		}
		if result.err == nil {
			w.recordOutcome("success")
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
		terminalOutcome := !result.retryable
		delay := min(backoff, w.maxBackoff)
		if !terminalOutcome && attempt < w.maxRetries {
			if requested, valid, exceeds := retryAfterDelay(result.retryAfter, time.Now(), w.maxBackoff); exceeds {
				terminalOutcome = true
			} else if valid {
				delay = max(delay, requested)
			}
		}
		if terminalOutcome || attempt == w.maxRetries {
			outcome := "exhausted"
			if terminalOutcome {
				outcome = "terminal"
			}
			w.recordOutcome(outcome)
			logger.Error(
				"webhook delivery failed, giving up",
				"attempts",
				attempt+1,
				"url",
				sanitizeURL(w.url),
				"event_type",
				e.Type,
				"error",
				result.err,
			)
			if !w.TrySendError(
				fmt.Errorf(
					"webhook delivery to %s failed after %d attempts: %w",
					sanitizeURL(w.url),
					attempt+1,
					result.err,
				),
			) {
				logger.Warn("could not send error to error channel (full)")
			}
			return
		}
		logger.Warn(
			"webhook delivery failed, retrying",
			"attempt",
			attempt+1,
			"max_retries",
			w.maxRetries,
			"delay",
			delay,
			"url",
			sanitizeURL(w.url),
			"event_type",
			e.Type,
			"error",
			result.err,
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-doneChan:
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(
			time.Duration(float64(backoff)*w.backoffFactor),
			w.maxBackoff,
		)
	}
}

// Stop the webhook output
func (w *WebhookOutput) Stop() error {
	return w.Shutdown(plugin.ShutdownHooks{})
}

var _ plugin.ManagedPlugin = (*WebhookOutput)(nil)
