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
	"sync/atomic"
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

// TestDispatchNotificationUpdatesHistoryAndHonorsPreferences was
// removed when the inline dispatchNotification function was replaced
// with the notifications.Engine + notifications.Dispatch pipeline. The
// equivalent coverage now lives in:
//   - tray/notifications/notify_test.go     (Dispatch ⇒ SendNotification)
//   - tray/notifications/engine_test.go     (rule matching, dedup,
//                                            rate limiting, connection
//                                            bypass)
//   - tray/notifications/rules_test.go      (per-template rule fan-out)
//   - tray/notifications/render_test.go     (Cardano-aware template
//                                            rendering)
//   - TestAddRecentEventKeepsNewestTen below (recent-events history)

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
		trayApp.Shutdown()
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

// TestShutdownIsIdempotent guards the sync.Once wrap: multiple callers
// (the tray Quit menu, the OS signal handler, test cleanup) must be
// able to invoke Shutdown without a double-close panic on quitChan.
func TestShutdownIsIdempotent(t *testing.T) {
	app := test.NewApp()
	trayApp := &App{
		fyneApp:  app,
		quitChan: make(chan struct{}),
	}

	assert.NotPanics(t, func() {
		trayApp.Shutdown()
		trayApp.Shutdown()
		trayApp.Shutdown()
	})
}

// TestShutdownWaitsForProducer is the regression guard for the
// shutdown-ordering finding: Shutdown must wait for the producer
// goroutine to exit before stopping the engine, otherwise shutdown-
// time RecordDrop calls conflate with backpressure drops and producer
// fyne.Do work can race against a torn-down driver. We synthesise the
// race with a slow producer that increments a counter after quitChan
// closes, then assert Shutdown sees the counter at its final value.
func TestShutdownWaitsForProducer(t *testing.T) {
	app := test.NewApp()
	trayApp := &App{
		fyneApp:  app,
		quitChan: make(chan struct{}),
		producerDone: func() chan struct{} {
			c := make(chan struct{})
			// Stand-in producer: closes producerDone 50ms after
			// quitChan closes. If Shutdown does NOT wait, it
			// returns immediately and the test races on `done`.
			go func() {
				<-c // never fires; we close producerDone manually
			}()
			return c
		}(),
	}

	var producerExited atomic.Bool
	go func() {
		<-trayApp.quitChan
		time.Sleep(50 * time.Millisecond)
		producerExited.Store(true)
		close(trayApp.producerDone)
	}()

	trayApp.Shutdown()
	assert.True(t, producerExited.Load(),
		"Shutdown must wait for producerDone before returning")
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
	t.Cleanup(func() { trayApp.Shutdown() })

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

// TestSuppressInitialFire is the regression guard for the review
// finding that the OnChange initial-fire suppression was unconditional
// (it would silently swallow a real first-event StatusError or
// StatusConnected if the tracker had transitioned before OnChange was
// registered, e.g. an auto-connect in NewApp). After the fix, only
// the default StatusStopped first-fire is swallowed; every other
// initial status flows through to the user.
func TestSuppressInitialFire(t *testing.T) {
	cases := []struct {
		name        string
		first       bool
		status      Status
		wantSuppress bool
		wantFirst    bool // value of `first` after the call
	}{
		{
			name: "first fire with default Stopped is suppressed",
			first: true, status: StatusStopped,
			wantSuppress: true, wantFirst: false,
		},
		{
			name: "first fire with Error is NOT suppressed",
			first: true, status: StatusError,
			wantSuppress: false, wantFirst: false,
		},
		{
			name: "first fire with Connected is NOT suppressed",
			first: true, status: StatusConnected,
			wantSuppress: false, wantFirst: false,
		},
		{
			name: "subsequent Stopped fire is NOT suppressed",
			first: false, status: StatusStopped,
			wantSuppress: false, wantFirst: false,
		},
		{
			name: "subsequent Error fire is NOT suppressed",
			first: false, status: StatusError,
			wantSuppress: false, wantFirst: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var first atomic.Bool
			first.Store(tc.first)
			got := suppressInitialFire(&first, tc.status)
			if got != tc.wantSuppress {
				t.Fatalf(
					"suppressInitialFire(%v, %v) = %v; want %v",
					tc.first, tc.status, got, tc.wantSuppress,
				)
			}
			if first.Load() != tc.wantFirst {
				t.Fatalf(
					"first after call = %v; want %v",
					first.Load(), tc.wantFirst,
				)
			}
		})
	}
}
