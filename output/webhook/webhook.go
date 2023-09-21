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
	"time"

	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/input/chainsync"
	"github.com/blinklabs-io/snek/internal/logging"
	"github.com/blinklabs-io/snek/internal/version"
)

type WebhookOutput struct {
	errorChan chan error
	eventChan chan event.Event
	url       string
	username  string
	password  string
}

func New(options ...WebhookOptionFunc) *WebhookOutput {
	w := &WebhookOutput{
		errorChan: make(chan error),
		eventChan: make(chan event.Event, 10),
		url:       "http://localhost:3000",
	}
	for _, option := range options {
		option(w)
	}
	return w
}

// Start the webhook output
func (w *WebhookOutput) Start() error {
	logger := logging.GetLogger()
	logger.Infof("starting webhook server")
	go func() {
		for {
			evt, ok := <-w.eventChan
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			payload := evt.Payload
			if payload == nil {
				panic(fmt.Errorf("ERROR: %v", payload))
			}
			switch evt.Type {
			case "chainsync.block":
				be := payload.(chainsync.BlockEvent)
				evt.Payload = be
			case "chainsync.rollback":
				re := payload.(chainsync.RollbackEvent)
				evt.Payload = re
			case "chainsync.transaction":
				te := payload.(chainsync.TransactionEvent)
				evt.Payload = te
			default:
				logger.Errorf("unknown event type: %s", evt.Type)
				return
			}
			// TODO: error handle
			err := w.SendWebhook(&evt)
			if err != nil {
				logger.Errorf("ERROR: %s", err)
			}
		}
	}()
	return nil
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

func (w *WebhookOutput) SendWebhook(e *event.Event) error {
	logger := logging.GetLogger()
	logger.Infof("sending event %s to %s", e.Type, w.url)
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	// Setup request
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", fmt.Sprintf("Snek/%s", version.GetVersionString()))

	// Setup authorization
	if w.username != "" && w.password != "" {
		req.Header.Add("Authorization", basicAuth(w.username, w.password))
	}
	// Setup custom transport to ignore self-signed SSL
	defaultTransport := http.DefaultTransport.(*http.Transport)
	customTransport := &http.Transport{
		Proxy:                 defaultTransport.Proxy,
		DialContext:           defaultTransport.DialContext,
		MaxIdleConns:          defaultTransport.MaxIdleConns,
		IdleConnTimeout:       defaultTransport.IdleConnTimeout,
		ExpectContinueTimeout: defaultTransport.ExpectContinueTimeout,
		TLSHandshakeTimeout:   defaultTransport.TLSHandshakeTimeout,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: customTransport}
	// Send payload
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	defer resp.Body.Close()

	logger.Infof("sent: %s, payload: %s, body: %s, response: %s, status: %d",
		w.url,
		string(data),
		string(respBody),
		resp.Status,
		resp.StatusCode,
	)
	return nil
}

// Stop the embedded output
func (w *WebhookOutput) Stop() error {
	close(w.eventChan)
	close(w.errorChan)
	return nil
}

// ErrorChan returns the input error channel
func (w *WebhookOutput) ErrorChan() chan error {
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
