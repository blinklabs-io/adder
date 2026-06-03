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
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type desktopTestApp struct {
	fyne.App
	mu   sync.RWMutex
	menu *fyne.Menu
	icon fyne.Resource
}

func (a *desktopTestApp) SetSystemTrayMenu(menu *fyne.Menu) {
	a.menu = menu
}

func (a *desktopTestApp) SetSystemTrayIcon(icon fyne.Resource) {
	a.mu.Lock()
	a.icon = icon
	a.mu.Unlock()
}

func (a *desktopTestApp) getIcon() fyne.Resource {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.icon
}

func (a *desktopTestApp) SetSystemTrayWindow(fyne.Window) {}

func TestStatusIconsReturnResources(t *testing.T) {
	for _, status := range []Status{
		StatusStopped,
		StatusStarting,
		StatusConnected,
		StatusReconnecting,
		StatusError,
		Status(99),
	} {
		t.Run(status.String(), func(t *testing.T) {
			icon := GetStatusIcon(status)
			require.NotNil(t, icon)
			assert.NotEmpty(t, icon.Name())
			assert.NotEmpty(t, icon.Content())
		})
	}

	assert.NotEmpty(t, DefaultIconBytes())
}

func TestEmojiAndExplorerURLHelpers(t *testing.T) {
	assert.Equal(t, "🧱", getEmojiForType("input.block"))
	assert.Equal(t, "💸", getEmojiForType("input.transaction"))
	assert.Equal(t, "🗳️", getEmojiForType("input.governance"))
	assert.Equal(t, "🔄", getEmojiForType("input.rollback"))
	assert.Equal(t, "❓", getEmojiForType("input.unknown"))

	tests := []struct {
		name string
		evt  event.Event
		hash string
		want string
	}{
		{
			name: "mainnet block by default",
			evt:  event.Event{Type: "input.block"},
			hash: "abc",
			want: "https://cexplorer.io/block/abc",
		},
		{
			name: "preprod transaction",
			evt: event.Event{
				Type:    "input.transaction",
				Context: map[string]any{"networkMagic": float64(1)},
			},
			hash: "def",
			want: "https://preprod.cexplorer.io/tx/def",
		},
		{
			name: "preview block",
			evt: event.Event{
				Type:    "input.block",
				Context: map[string]any{"networkMagic": float64(2)},
			},
			hash: "123",
			want: "https://preview.cexplorer.io/block/123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, getExplorerURL(tc.evt, tc.hash))
		})
	}
}

func TestDispatchNotificationUpdatesHistoryAndHonorsPreferences(t *testing.T) {
	app := test.NewApp()
	trayApp := &App{
		config: TrayConfig{
			NotifyPrefs: map[string]bool{
				setup.NotifyPrefBlocksMinted: true,
				setup.NotifyPrefIncomingTx:   true,
				setup.NotifyPrefVotesCast:    true,
			},
		},
		fyneApp: app,
		mRecent: fyne.NewMenuItem("Recent Events", nil),
		mMenu:   fyne.NewMenu("Adder"),
	}

	blockEvt := event.Event{
		Type:      "input.block",
		Timestamp: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		Payload:   map[string]any{"blockHash": "blockhash"},
	}
	test.AssertNotificationSent(
		t,
		fyne.NewNotification("🧱 New Block", "Block Hash: blockhash"),
		func() { trayApp.dispatchNotification(blockEvt) },
	)

	txEvt := event.Event{
		Type:    "input.transaction",
		Payload: map[string]any{"transactionHash": "txhash"},
	}
	test.AssertNotificationSent(
		t,
		fyne.NewNotification("💸 New Transaction", "Hash: txhash"),
		func() { trayApp.dispatchNotification(txEvt) },
	)

	govEvt := event.Event{Type: "input.governance"}
	test.AssertNotificationSent(
		t,
		fyne.NewNotification("🗳️ Governance Action", "A new governance action was detected."),
		func() { trayApp.dispatchNotification(govEvt) },
	)

	unknownEvt := event.Event{Type: "input.unknown"}
	test.AssertNotificationSent(t, nil, func() {
		trayApp.dispatchNotification(unknownEvt)
	})

	require.Eventually(t, func() bool {
		return len(trayApp.recentEvents) == 4 &&
			trayApp.mRecent.ChildMenu != nil &&
			len(trayApp.mRecent.ChildMenu.Items) == 4
	}, time.Second, 10*time.Millisecond)
	assert.Contains(t, trayApp.mRecent.ChildMenu.Items[0].Label, "unknown")
}

func TestAddRecentEventKeepsNewestTen(t *testing.T) {
	test.NewApp()
	trayApp := &App{
		mRecent: fyne.NewMenuItem("Recent Events", nil),
		mMenu:   fyne.NewMenu("Adder"),
	}

	for i := 0; i < 12; i++ {
		trayApp.addRecentEvent(event.Event{
			Type:      "input.block",
			Timestamp: time.Date(2026, 5, 28, 12, 0, i, 0, time.UTC),
			Payload:   map[string]any{"blockHash": "hash"},
		})
	}

	require.Eventually(t, func() bool {
		return len(trayApp.recentEvents) == 10 &&
			trayApp.mRecent.ChildMenu != nil &&
			len(trayApp.mRecent.ChildMenu.Items) == 10
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 11, trayApp.recentEvents[0].Timestamp.Second())
	assert.Equal(t, 2, trayApp.recentEvents[9].Timestamp.Second())
}

func TestSetupTrayBuildsDesktopMenu(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command fakes differ on Windows")
	}
	installFakeTrayCommands(t)
	configDir := t.TempDir()
	logDir := t.TempDir()
	t.Setenv("ADDER_TRAY_CONFIG_DIR", configDir)
	t.Setenv("ADDER_TRAY_LOG_DIR", logDir)

	baseApp := test.NewApp()
	deskApp := &desktopTestApp{App: baseApp}
	reconfigurePlan := setup.SetupPlan{
		Network: setup.NetworkConfig{Name: "preview"},
		Filter:  setup.FilterConfig{MonitorEverything: true},
		API:     setup.APIConfig{Address: "127.0.0.1", Port: 8080},
		Output: setup.OutputConfig{
			Type:   "none",
			Config: make(map[string]string),
		},
	}
	trayApp := &App{
		config: TrayConfig{
			AdderConfig: filepath.Join(configDir, "config.yaml"),
			NotifyPrefs: map[string]bool{
				setup.NotifyPrefConnectionIssues: true,
			},
		},
		fyneApp:  deskApp,
		conn:     NewConnectionManager(),
		runner:   &setup.SetupRunner{Store: &reconfigureStore{engine: reconfigurePlan.ToEngineConfig(*config.GetConfig())}},
		quitChan: make(chan struct{}),
	}
	t.Cleanup(func() {
		trayApp.conn.Disconnect()
		close(trayApp.quitChan)
	})

	trayApp.setupTray()

	require.NotNil(t, deskApp.menu)
	assert.Equal(t, "Adder", deskApp.menu.Label)
	assert.NotNil(t, trayApp.mRecent)
	assert.NotNil(t, trayApp.mMenu)

	trayApp.recentEvents = []event.Event{{Type: "input.block"}}
	clearItem := deskApp.menu.Items[3]
	require.Equal(t, "Clear History", clearItem.Label)
	clearItem.Action()
	require.Eventually(t, func() bool {
		return len(trayApp.recentEvents) == 0
	}, time.Second, 10*time.Millisecond)

	deskApp.menu.Items[5].Action()
	require.Eventually(t, func() bool {
		return trayApp.conn.connected
	}, time.Second, 10*time.Millisecond)

	deskApp.menu.Items[6].Action()
	require.Eventually(t, func() bool {
		return !trayApp.conn.connected && trayApp.intentionalStop.Load()
	}, time.Second, 10*time.Millisecond)

	deskApp.menu.Items[7].Action()
	require.Eventually(t, func() bool {
		return trayApp.conn.connected && !trayApp.intentionalStop.Load()
	}, time.Second, 10*time.Millisecond)

	deskApp.menu.Items[8].Action()
	deskApp.menu.Items[10].Action()
	deskApp.menu.Items[11].Action()
	deskApp.menu.Items[13].Action()
	deskApp.menu.Items[14].Action()

	trayApp.conn.status.Set(StatusError)
	require.Eventually(t, func() bool {
		return deskApp.getIcon() != nil
	}, time.Second, 10*time.Millisecond)
}

func TestShutdownClosesQuitChannel(t *testing.T) {
	app := test.NewApp()
	trayApp := &App{
		fyneApp:  app,
		quitChan: make(chan struct{}),
	}

	trayApp.Shutdown()

	select {
	case <-trayApp.quitChan:
	case <-time.After(time.Second):
		t.Fatal("quit channel was not closed")
	}
}

func TestOpenFolderIgnoresEmptyPath(t *testing.T) {
	openFolder("")
}

func TestRunConfiguresTrayAndRunsApp(t *testing.T) {
	baseApp := test.NewApp()
	deskApp := &desktopTestApp{App: baseApp}
	trayApp := &App{
		config:   DefaultConfig(),
		fyneApp:  deskApp,
		conn:     NewConnectionManager(),
		quitChan: make(chan struct{}),
	}
	t.Cleanup(func() { close(trayApp.quitChan) })

	trayApp.Run()

	require.NotNil(t, deskApp.menu)
}

func TestOnWizardFinishReloadsTrayConfigOnSuccess(t *testing.T) {
	app := test.NewApp()
	store := &wizardFinishStore{
		tray: setup.TrayConfig{
			APIAddress:  "127.0.0.1",
			APIPort:     9090,
			AdderConfig: "/tmp/config.yaml",
			NotifyPrefs: make(map[string]bool),
		},
	}
	trayApp := &App{
		config:  DefaultConfig(),
		fyneApp: app,
		runner: &setup.SetupRunner{
			Store:   store,
			Service: &wizardFinishService{},
			Conn:    &wizardFinishConn{},
			Finder:  &wizardFinishFinder{path: "/tmp/adder"},
		},
	}

	trayApp.onWizardFinish(context.Background(), setup.SetupPlan{
		Network: setup.NetworkConfig{Name: "mainnet"},
		Filter:  setup.FilterConfig{MonitorEverything: true},
		API:     setup.APIConfig{Address: "127.0.0.1", Port: 9090},
		Output:  setup.OutputConfig{Type: "none", Config: make(map[string]string)},
		Notify:  make(setup.NotificationPrefs),
	}, nil)

	require.Eventually(t, func() bool {
		return trayApp.Config().APIPort == 9090
	}, 2*time.Second, 10*time.Millisecond)
}

type wizardFinishStore struct {
	engine config.Config
	tray   setup.TrayConfig
}

func (s *wizardFinishStore) LoadEngine(string) (config.Config, error) {
	return s.engine, nil
}

func (s *wizardFinishStore) SaveEngineAtomic(_ string, cfg config.Config) error {
	s.engine = cfg
	return nil
}

func (s *wizardFinishStore) LoadTray() (setup.TrayConfig, error) {
	return s.tray, nil
}

func (s *wizardFinishStore) SaveTrayAtomic(cfg setup.TrayConfig) error {
	s.tray = cfg
	return nil
}

type wizardFinishService struct{}

func (s *wizardFinishService) EnsureRegistered(string, string) error { return nil }
func (s *wizardFinishService) EnsureRunning() error                  { return nil }
func (s *wizardFinishService) RestartIfConfigChanged(string, string) error {
	return nil
}
func (s *wizardFinishService) Stop() error { return nil }
func (s *wizardFinishService) Status() (setup.ServiceStatus, error) {
	return setup.ServiceRunning, nil
}

type wizardFinishConn struct{}

func (c *wizardFinishConn) Connect() error    { return nil }
func (c *wizardFinishConn) Disconnect()       {}
func (c *wizardFinishConn) Reconnect() error  { return nil }
func (c *wizardFinishConn) SetAddress(string) {}
func (c *wizardFinishConn) SetPort(uint)      {}

type wizardFinishFinder struct {
	path string
}

func (f *wizardFinishFinder) Find() (string, error) {
	return f.path, nil
}

func installFakeTrayCommands(t *testing.T) {
	t.Helper()

	names := []string{"xdg-open", "systemctl"}
	if runtime.GOOS == "darwin" {
		names = []string{"open", "launchctl"}
	}

	binDir := t.TempDir()
	for _, name := range names {
		require.NoError(t, os.WriteFile(
			filepath.Join(binDir, name),
			[]byte("#!/bin/sh\nexit 0\n"),
			0o755,
		))
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
