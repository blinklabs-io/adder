// Copyright 2026 Blink Labs Software
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
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRoundTripper func(*http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

type mockTokenProvider struct {
	token string
	err   error
}

func (m *mockTokenProvider) GetToken(context.Context) (string, error) {
	return m.token, m.err
}

type blockingTokenProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *blockingTokenProvider) GetToken(ctx context.Context) (string, error) {
	close(p.entered)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.release:
		return "token", nil
	}
}

func createDummyCredentialsFile(t *testing.T, projectID string) string {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/service-account.json"
	content := []byte(`{"project_id": "` + projectID + `"}`)
	err := os.WriteFile(filePath, content, 0o644)
	require.NoError(t, err)
	return filePath
}

func TestPushOutput_FCMFailureSurfacesError(t *testing.T) {
	// Add a test token to the package-level registry
	token := "test-token-1234"
	if fcmStore == nil {
		t.Fatal("fcmStore is nil")
	}
	fcmStore.mu.Lock()
	fcmStore.FCMTokens[token] = token
	fcmStore.mu.Unlock()
	t.Cleanup(func() {
		if fcmStore == nil {
			return
		}
		fcmStore.mu.Lock()
		delete(fcmStore.FCMTokens, token)
		fcmStore.mu.Unlock()
	})

	credentialsPath := createDummyCredentialsFile(t, "test-fcm-project")
	p, err := New(
		WithServiceAccountFilePath(credentialsPath),
		WithTokenProvider(&mockTokenProvider{token: "mock-access-token"}),
	)
	require.NoError(t, err)

	err = p.Start()
	require.NoError(t, err)
	defer p.Stop()

	// Mock http.DefaultTransport to return a 500 error on FCM requests
	origTransport := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = origTransport
	})

	http.DefaultTransport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewBufferString("FCM Simulated 500 Error")),
			Header:     make(http.Header),
		}, nil
	})

	// Send a block event
	evt := event.Event{
		Type: event.TypeBlock,
		Context: event.BlockContext{
			BlockNumber: 100,
			SlotNumber:  200,
		},
		Payload: event.BlockEvent{
			BlockHash: "test-hash",
		},
	}

	p.InputChan() <- evt

	// Assert the error propagates on the ErrorChan within 2 seconds
	select {
	case err := <-p.ErrorChan():
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send message to token")
		assert.Contains(t, err.Error(), "FCM Simulated 500 Error")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for FCM failure error propagation")
	}
}

func TestPushOutput_New_EmptyServiceAccountFilePath_Fails(t *testing.T) {
	_, err := New(WithServiceAccountFilePath(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service account file path is required")
}

func TestPushOutput_New_NonexistentFile_Fails(t *testing.T) {
	_, err := New(WithServiceAccountFilePath("nonexistent-file-path-123.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read credential file")
}

func TestPushOutput_New_InvalidJSON_Fails(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/bad.json"
	err := os.WriteFile(filePath, []byte("{invalid-json}"), 0o644)
	require.NoError(t, err)

	_, err = New(WithServiceAccountFilePath(filePath))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse credential file")
}

func TestPushOutput_New_MissingProjectId_Fails(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := tmpDir + "/missing-id.json"
	err := os.WriteFile(filePath, []byte(`{"not_project_id": "value"}`), 0o644)
	require.NoError(t, err)

	_, err = New(WithServiceAccountFilePath(filePath))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid or empty project_id in service account file")
}

func TestPushOutput_ShutdownDuringInFlightRequest(t *testing.T) {
	token := "test-token-5678"
	if fcmStore == nil {
		t.Fatal("fcmStore is nil")
	}
	fcmStore.mu.Lock()
	fcmStore.FCMTokens[token] = token
	fcmStore.mu.Unlock()
	t.Cleanup(func() {
		if fcmStore == nil {
			return
		}
		fcmStore.mu.Lock()
		delete(fcmStore.FCMTokens, token)
		fcmStore.mu.Unlock()
	})

	credentialsPath := createDummyCredentialsFile(t, "test-fcm-project")
	p, err := New(
		WithServiceAccountFilePath(credentialsPath),
		WithTokenProvider(&mockTokenProvider{token: "mock-access-token"}),
	)
	require.NoError(t, err)

	err = p.Start()
	require.NoError(t, err)

	origTransport := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = origTransport
	})

	requestStarted := make(chan struct{})
	http.DefaultTransport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})

	// Send an event
	evt := event.Event{
		Type: event.TypeBlock,
		Context: event.BlockContext{
			BlockNumber: 100,
			SlotNumber:  200,
		},
		Payload: event.BlockEvent{
			BlockHash: "test-hash",
		},
	}

	p.InputChan() <- evt

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("FCM request did not start")
	}

	// Concurrently call Stop() while the request is in-flight.
	// If the race detector or panic bug is present, this will trigger them.
	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the in-flight FCM request")
	}
}

func TestPushOutput_StopCancelsTokenAcquisition(t *testing.T) {
	provider := &blockingTokenProvider{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(provider.release)

	credentialsPath := createDummyCredentialsFile(t, "test-fcm-project")
	p, err := New(
		WithServiceAccountFilePath(credentialsPath),
		WithTokenProvider(provider),
	)
	require.NoError(t, err)
	require.NoError(t, p.Start())

	p.InputChan() <- event.Event{
		Type:    event.TypeRollback,
		Payload: event.RollbackEvent{},
	}
	select {
	case <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("token acquisition did not start")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel token acquisition")
	}
}
