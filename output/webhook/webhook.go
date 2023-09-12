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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/input/chainsync"
	"github.com/blinklabs-io/snek/internal/logging"
	"github.com/blinklabs-io/snek/internal/version"
)

type WebhookOutput struct {
	errorChan chan error
	eventChan chan event.Event
	url       string
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
			logger.Infof("debug: type: %s", evt.Type)
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
			err := SendWebhook(&evt, w.url)
			if err != nil {
				logger.Errorf("ERROR: %s", err)
			}
		}
	}()
	return nil
}

func SendWebhook(e *event.Event, url string) error {
	logger := logging.GetLogger()
	logger.Infof("sending event %s to %s", e.Type, url)
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	// Setup request
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", fmt.Sprintf("Snek/%s", version.GetVersionString()))
	// Send payload
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s", err)
	}
	defer resp.Body.Close()

	logger.Infof("sent: %s, payload: %s, body: %s, response: %s, status: %d",
		url,
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
