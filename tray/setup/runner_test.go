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

package setup

import (
	"context"
	"errors"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	engine        config.Config
	tray          TrayConfig
	saved         bool
	loadEngineErr error
	saveEngineErr error
	saveTrayErr   error
}

func (m *mockStore) LoadEngine(path string) (config.Config, error) {
	if m.loadEngineErr != nil {
		return config.Config{}, m.loadEngineErr
	}
	return m.engine, nil
}
func (m *mockStore) SaveEngineAtomic(path string, cfg config.Config) error {
	if m.saveEngineErr != nil {
		return m.saveEngineErr
	}
	m.engine = cfg
	m.saved = true
	return nil
}
func (m *mockStore) LoadTray() (TrayConfig, error) { return m.tray, nil }
func (m *mockStore) SaveTrayAtomic(cfg TrayConfig) error {
	if m.saveTrayErr != nil {
		return m.saveTrayErr
	}
	m.tray = cfg
	return nil
}

type mockService struct {
	registered bool
	running    bool
	restarts   int
}

func (m *mockService) EnsureRegistered(bin, cfg string) error { m.registered = true; return nil }
func (m *mockService) EnsureRunning() error                   { m.running = true; return nil }
func (m *mockService) RestartIfConfigChanged(bin, cfg string) error {
	m.restarts++
	return nil
}
func (m *mockService) Stop() error                    { m.running = false; return nil }
func (m *mockService) Status() (ServiceStatus, error) { return ServiceRunning, nil }

type mockConnector struct {
	connected    bool
	address      string
	port         uint
	reconnectErr error
}

func (m *mockConnector) Connect() error { m.connected = true; return nil }
func (m *mockConnector) Disconnect()    { m.connected = false }
func (m *mockConnector) Reconnect() error {
	if m.reconnectErr != nil {
		return m.reconnectErr
	}
	m.connected = true
	return nil
}
func (m *mockConnector) SetAddress(addr string) { m.address = addr }
func (m *mockConnector) SetPort(port uint)      { m.port = port }

type mockFinder struct {
	path string
	err  error
}

func (m *mockFinder) Find() (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.path, nil
}

func TestApplyDoesNotTouchHostServicesWithFakeManager(t *testing.T) {
	store := &mockStore{}
	svc := &mockService{}
	conn := &mockConnector{}
	finder := &mockFinder{path: "/tmp/adder"}
	runner := &SetupRunner{Store: store, Service: svc, Conn: conn, Finder: finder}

	plan := SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
		API:     APIConfig{Address: "127.0.0.1", Port: 8080},
	}

	err := runner.Apply(context.Background(), plan)
	require.NoError(t, err)

	assert.True(t, store.saved)
	assert.Equal(t, 1, svc.restarts)
	assert.True(t, conn.connected)
	assert.Equal(t, "127.0.0.1", conn.address)
	assert.Equal(t, uint(8080), conn.port)
}

func TestApplyReturnsStoreErrorsBeforeServiceWork(t *testing.T) {
	wantErr := errors.New("boom")
	tests := []struct {
		name  string
		store *mockStore
	}{
		{name: "load engine", store: &mockStore{loadEngineErr: wantErr}},
		{name: "save engine", store: &mockStore{saveEngineErr: wantErr}},
		{name: "save tray", store: &mockStore{saveTrayErr: wantErr}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockService{}
			runner := &SetupRunner{
				Store:   tc.store,
				Service: svc,
				Conn:    &mockConnector{},
				Finder:  &mockFinder{path: "/tmp/adder"},
			}

			err := runner.Apply(context.Background(), SetupPlan{
				Network: NetworkConfig{Name: "mainnet"},
				Filter:  FilterConfig{MonitorEverything: true},
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, wantErr)
			assert.False(t, svc.running)
		})
	}
}

func TestApplyContinuesWhenBinaryFinderFails(t *testing.T) {
	store := &mockStore{}
	conn := &mockConnector{}
	runner := &SetupRunner{
		Store:   store,
		Service: &mockService{},
		Conn:    conn,
		Finder:  &mockFinder{err: errors.New("not found")},
	}

	err := runner.Apply(context.Background(), SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
	})
	require.NoError(t, err)

	assert.True(t, store.saved)
	assert.True(t, conn.connected)
}

func TestApplyHonorsContextCancellationBeforeReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := &mockStore{}
	conn := &mockConnector{}
	runner := &SetupRunner{
		Store:   store,
		Service: &mockService{},
		Conn:    conn,
		Finder:  &mockFinder{path: "/tmp/adder"},
	}

	err := runner.Apply(ctx, SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
	})
	require.ErrorIs(t, err, context.Canceled)

	assert.True(t, store.saved)
	assert.False(t, conn.connected)
}
