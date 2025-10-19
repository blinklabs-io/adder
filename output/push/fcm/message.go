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

package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/blinklabs-io/adder/internal/logging"
)

type Message struct {
	MessageContent `json:"message"`
}

type MessageContent struct {
	Notification *NotificationContent `json:"notification,omitempty"`
	Data         map[string]any       `json:"data,omitempty"`
	Token        string               `json:"token"`
}

type NotificationContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type MessageOption func(*MessageContent)

func WithData(data map[string]any) MessageOption {
	return func(m *MessageContent) {
		m.Data = data
	}
}

func WithNotification(title string, body string) MessageOption {
	return func(m *MessageContent) {
		m.Notification = &NotificationContent{
			Title: title,
			Body:  body,
		}
	}
}

func NewMessage(token string, opts ...MessageOption) *Message {
	if token == "" {
		logging.GetLogger().Error("Token is mandatory for FCM message")
		os.Exit(1)
	}

	msg := &Message{
		MessageContent: MessageContent{
			Token: token,
		},
	}
	for _, opt := range opts {
		opt(&msg.MessageContent)
	}
	return msg
}

func Send(accessToken string, projectId string, msg *Message) error {
	fcmEndpoint := fmt.Sprintf(
		"https://fcm.googleapis.com/v1/projects/%s/messages:send",
		projectId,
	)

	// Convert the message to JSON
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	fmt.Println(string(payload))

	// Create a new HTTP request
	ctx := context.Background()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fcmEndpoint,
		bytes.NewBuffer(payload),
	)
	if err != nil {
		return err
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	// Execute the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("failed to send payload to fcm: %s", payload)
	}
	defer resp.Body.Close()

	// Check for errors in the response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.New(string(body))
	}

	return nil
}
