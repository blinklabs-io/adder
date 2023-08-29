package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"golang.org/x/oauth2/google"
)

// TODO Fix log usage

const (
	scopeURL = "https://www.googleapis.com/auth/firebase.messaging"
	// Get file serviceaccount file from config
	credFile = ""
)

type Message struct {
	MessageContent `json:"message"`
}

type MessageContent struct {
	Token        string                 `json:"token"`
	Notification *NotificationContent   `json:"notification,omitempty"`
	Data         map[string]interface{} `json:"data,omitempty"`
}

type NotificationContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type MessageOption func(*MessageContent)

func WithData(data map[string]interface{}) MessageOption {
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
		log.Fatalf("Token is mandatory for FCM message")
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

func Send(token string, msg *Message) error {
	// TODO Parse project id from service-account json
	projectId := ""
	fcmEndpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", projectId)

	// Convert the message to JSON
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	fmt.Println(string(payload))

	// Create a new HTTP request
	req, err := http.NewRequest("POST", fcmEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	// Execute the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for errors in the response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return errors.New(string(body))
	}

	return nil
}

func GetAccessToken() string {
	data, err := os.ReadFile(credFile)
	if err != nil {
		log.Fatalf("Failed to read the credential file: %v", err)
	}

	conf, err := google.JWTConfigFromJSON(data, scopeURL)
	if err != nil {
		log.Fatalf("Failed to parse the credential file: %v", err)
	}

	token, err := conf.TokenSource(context.Background()).Token()
	if err != nil {
		log.Fatalf("Failed to get token: %v", err)
	}

	fmt.Println(token.AccessToken)
	return token.AccessToken
}
