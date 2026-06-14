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

	_, err := runner.Apply(context.Background(), plan)
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

			_, err := runner.Apply(context.Background(), SetupPlan{
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

	_, err := runner.Apply(context.Background(), SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
	})
	require.NoError(t, err)

	assert.True(t, store.saved)
	assert.True(t, conn.connected)
}

// TestApplyDoesNotAliasNotifyPrefs is the regression guard for the
// codec-side invariant (also enforced by SetupPlanFromEngineConfig)
// that TrayConfig.NotifyPrefs must not share its map header with
// plan.Notify. A type conversion `map[string]bool(plan.Notify)` would
// produce an alias, so later mutations of either map would silently
// corrupt the other. The runner explicitly copies on save.
func TestApplyDoesNotAliasNotifyPrefs(t *testing.T) {
	store := &mockStore{}
	runner := &SetupRunner{
		Store:   store,
		Service: &mockService{},
		Conn:    &mockConnector{},
		Finder:  &mockFinder{path: "/tmp/adder"},
	}
	plan := SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
		Notify: NotificationPrefs{
			"Blocks minted": true,
		},
	}
	_, applyErr := runner.Apply(context.Background(), plan)
	require.NoError(t, applyErr)

	// Mutate the plan's map post-Apply; the saved TrayConfig's map
	// must NOT see the change.
	plan.Notify["Blocks minted"] = false
	plan.Notify["Injected after Apply"] = true

	assert.True(t, store.tray.NotifyPrefs["Blocks minted"],
		"mutation of plan.Notify must not leak into the saved tray "+
			"config (alias bug)")
	_, leaked := store.tray.NotifyPrefs["Injected after Apply"]
	assert.False(t, leaked,
		"new keys added to plan.Notify post-Apply must not appear "+
			"in the saved tray config")
}

// TestApplyDoesNotAliasFilterSlices mirrors the codec-side check: the
// runner must CloneFilter when persisting plan.Filter into TrayConfig,
// not pass the struct header through verbatim. A future append-grow
// on plan.Filter.Wallets would otherwise mutate the persisted
// TrayConfig's slice.
func TestApplyDoesNotAliasFilterSlices(t *testing.T) {
	store := &mockStore{}
	runner := &SetupRunner{
		Store:   store,
		Service: &mockService{},
		Conn:    &mockConnector{},
		Finder:  &mockFinder{path: "/tmp/adder"},
	}
	plan := SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter: FilterConfig{
			Wallets:  []string{"addr1a"},
			DReps:    []string{"drep1a"},
			Pools:    []string{"pool1a"},
			Assets:   []string{"asset1a"},
			Policies: []string{"pol1"},
		},
	}
	_, err := runner.Apply(context.Background(), plan)
	require.NoError(t, err)

	plan.Filter.Wallets[0] = "MUTATED"
	plan.Filter.DReps[0] = "MUTATED"
	plan.Filter.Pools[0] = "MUTATED"
	plan.Filter.Assets[0] = "MUTATED"
	plan.Filter.Policies[0] = "MUTATED"

	assert.Equal(t, "addr1a", store.tray.Filter.Wallets[0])
	assert.Equal(t, "drep1a", store.tray.Filter.DReps[0])
	assert.Equal(t, "pool1a", store.tray.Filter.Pools[0])
	assert.Equal(t, "asset1a", store.tray.Filter.Assets[0])
	assert.Equal(t, "pol1", store.tray.Filter.Policies[0])
}

// TestApplyReturnsBinaryFindAsApplyResult is the regression guard for
// the silent-success-on-soft-failure finding: when the binary cannot
// be located, the wizard previously claimed success and the operator
// only learned of the problem from a later "API unreachable" error
// stripped of context. After the fix, Apply returns a non-zero
// ApplyResult.BinaryFindErr that the wizard surfaces to the user.
func TestApplyReturnsBinaryFindAsApplyResult(t *testing.T) {
	wantErr := errors.New("not on PATH")
	runner := &SetupRunner{
		Store:   &mockStore{},
		Service: &mockService{},
		Conn:    &mockConnector{},
		Finder:  &mockFinder{err: wantErr},
	}
	result, err := runner.Apply(context.Background(), SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
	})
	require.NoError(t, err,
		"Apply must still succeed — config IS saved")
	assert.True(t, result.HasSoftErrors(),
		"BinaryFindErr should be surfaced as a soft error")
	assert.ErrorIs(t, result.BinaryFindErr, wantErr)
}

// TestApplyReconnectErrWrapsSoftErrors guards that a binary-find or
// restart failure followed by an "API unreachable" error produces a
// composite message naming the root cause, not just the downstream
// symptom.
func TestApplyReconnectErrWrapsSoftErrors(t *testing.T) {
	wantBin := errors.New("not on PATH")
	wantReconnect := errors.New("dial tcp: no route")
	runner := &SetupRunner{
		Store:   &mockStore{},
		Service: &mockService{},
		Conn:    &mockConnector{reconnectErr: wantReconnect},
		Finder:  &mockFinder{err: wantBin},
	}
	_, err := runner.Apply(context.Background(), SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantBin,
		"composite error must preserve the binary-find cause")
	assert.ErrorIs(t, err, wantReconnect,
		"composite error must also preserve the reconnect cause")
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

	_, err := runner.Apply(ctx, SetupPlan{
		Network: NetworkConfig{Name: "mainnet"},
		Filter:  FilterConfig{MonitorEverything: true},
	})
	require.ErrorIs(t, err, context.Canceled)

	assert.True(t, store.saved)
	assert.False(t, conn.connected)
}
