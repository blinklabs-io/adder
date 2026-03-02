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

package tray

import (
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionManager_ConnectDisconnect(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	cm := NewConnectionManager(
		WithConnectionAddress(address),
		WithConnectionPort(port),
	)

	require.NoError(t, cm.Connect())

	waitForStatus(t, cm.status, StatusConnected, 2*time.Second)
	assert.True(t, cm.IsConnected())

	cm.Disconnect()

	assert.Equal(t, StatusStopped, cm.status.Status())
	assert.False(t, cm.IsConnected())
}

func TestConnectionManager_EventsForwarded(t *testing.T) {
	serverURL, send, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	cm := NewConnectionManager(
		WithConnectionAddress(address),
		WithConnectionPort(port),
	)
	require.NoError(t, cm.Connect())
	defer cm.Disconnect()

	waitForStatus(t, cm.status, StatusConnected, 2*time.Second)

	testEvt := event.Event{
		Type:      "input.block",
		Timestamp: time.Now().Truncate(time.Millisecond),
		Payload:   map[string]string{"hash": "test123"},
	}
	send <- testEvt

	select {
	case received := <-cm.Events():
		assert.Equal(t, testEvt.Type, received.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded event")
	}
}

func TestConnectionManager_DoubleConnect(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	cm := NewConnectionManager(
		WithConnectionAddress(address),
		WithConnectionPort(port),
	)
	require.NoError(t, cm.Connect())
	defer cm.Disconnect()

	err := cm.Connect()
	assert.Error(t, err, "second Connect should return error")
}

func TestConnectionManager_Reconnect(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	cm := NewConnectionManager(
		WithConnectionAddress(address),
		WithConnectionPort(port),
	)
	require.NoError(t, cm.Connect())
	defer cm.Disconnect()

	waitForStatus(t, cm.status, StatusConnected, 2*time.Second)

	// Reconnect should disconnect then reconnect
	require.NoError(t, cm.Reconnect())

	waitForStatus(t, cm.status, StatusConnected, 2*time.Second)
	assert.True(t, cm.IsConnected())
}

func TestConnectionManager_DisconnectWithoutConnect(t *testing.T) {
	cm := NewConnectionManager()
	// Should not panic
	cm.Disconnect()
}

func TestConnectionManager_WithStatusTracker(t *testing.T) {
	serverURL, _, done := newTestWSServer(t)
	defer close(done)

	address, port := parseTestURL(t, serverURL)

	tracker := NewStatusTracker()
	cm := NewConnectionManager(
		WithConnectionAddress(address),
		WithConnectionPort(port),
		WithConnectionStatusTracker(tracker),
	)
	require.NoError(t, cm.Connect())
	defer cm.Disconnect()

	waitForStatus(t, tracker, StatusConnected, 2*time.Second)
}
