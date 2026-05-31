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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/ui/assets"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/blinklabs-io/adder/tray/wizard"
)

// App holds references to all major components of the tray
// application.
type App struct {
	configMu sync.RWMutex
	config   TrayConfig
	conn     *ConnectionManager
	fyneApp  fyne.App
	runner   *setup.SetupRunner

	// Cached icon paths for notifications
	blockIcon string
	govIcon   string
	txIcon    string

	// uiMu serializes the fyne.Do closures that mutate shared tray UI
	// state (status label, tray icon, recent-events menu). In production
	// the GLFW driver runs every fyne.Do body on the single main-loop
	// goroutine, but the test driver runs them inline on the calling
	// goroutine, so the status observer, icon watchdog and event
	// dispatcher would otherwise race. Every UI-mutating fyne.Do body
	// must hold this lock.
	uiMu sync.Mutex

	// Recent events for the tray menu
	recentEvents []event.Event
	mRecent      *fyne.MenuItem
	mMenu        *fyne.Menu

	// intentionalStop records whether the user deliberately stopped the
	// service, so a Stopped status is not reported as a lost connection.
	// Written from menu handlers and read from the status observer
	// goroutine, hence atomic.
	intentionalStop atomic.Bool
	quitChan        chan struct{}
}

// NewApp creates and initialises the tray application.
func NewApp(fyneApp fyne.App) (*App, error) {
	configPath := ConfigPath()
	configExists := ConfigExists()
	slog.Info("checking for existing configuration",
		"path", configPath,
		"exists", configExists)

	cfg, err := LoadConfig()
	if err != nil {
		slog.Warn(
			"failed to load config, using defaults",
			"error", err,
		)
		cfg = DefaultConfig()
	}

	// Prepare specialized icons for notifications
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		slog.Warn("failed to get user cache dir, falling back to temp", "error", err)
		cacheDir = os.TempDir()
	}
	adderCache := filepath.Join(cacheDir, "adder")
	if err := os.MkdirAll(adderCache, 0o700); err != nil {
		slog.Error("failed to create icon cache directory",
			"path", adderCache,
			"error", err)
	}

	blockPath := filepath.Join(adderCache, "block.png")
	govPath := filepath.Join(adderCache, "gov.png")
	txPath := filepath.Join(adderCache, "tx.png")

	slog.Debug("preparing specialized icons", "cache", adderCache)
	if err := os.WriteFile(blockPath, assets.GetBlockIcon(128).Content(), 0o600); err != nil {
		slog.Error("failed to write block icon", "path", blockPath, "error", err)
	}
	if err := os.WriteFile(govPath, assets.GetGovernanceIcon(128).Content(), 0o600); err != nil {
		slog.Error("failed to write governance icon", "path", govPath, "error", err)
	}
	if err := os.WriteFile(txPath, assets.GetTransactionIcon(128).Content(), 0o600); err != nil {
		slog.Error("failed to write transaction icon", "path", txPath, "error", err)
	}

	a := &App{
		config:    cfg,
		fyneApp:   fyneApp,
		blockIcon: blockPath,
		govIcon:   govPath,
		txIcon:    txPath,
		conn: NewConnectionManager(
			WithConnectionAddress(cfg.APIAddress),
			WithConnectionPort(cfg.APIPort),
		),
		quitChan: make(chan struct{}),
	}
	a.runner = &setup.SetupRunner{
		Store:   &setup.LocalStore{TrayConfigPath: ConfigPath()},
		Service: &setup.OSManager{},
		Conn:    a.conn,
		Finder:  &setup.AppBinaryFinder{},
	}

	// Show wizard on first run
	if !configExists {
		wizard.ShowWizard(nil, a.onWizardFinish)
	}

	return a, nil
}

// onWizardFinish is called when the setup wizard completes.
func (a *App) onWizardFinish(
	ctx context.Context,
	plan setup.SetupPlan,
	ctrl *wizard.WizardController,
) {
	slog.Info("wizard finished, running setup tasks")

	go func() {
		if err := a.runner.Apply(ctx, plan); err != nil {
			a.failTask("Failed to apply configuration", err, ctrl)
			return
		}
		if cfg, err := a.runner.Store.LoadTray(); err != nil {
			slog.Warn("failed to reload tray config after setup", "error", err)
		} else {
			a.configMu.Lock()
			a.config = cfg
			a.configMu.Unlock()
		}

		// Success!
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			if ctrl != nil {
				ctrl.Close()
			}
			successMsg := fmt.Sprintf(
				"Successfully connected to Adder API at %s:%d.\n\nMonitoring: %s",
				plan.API.Address,
				plan.API.Port,
				plan.Filter.Template,
			)

			// Show completion dialog if we have a window available
			if wins := a.fyneApp.Driver().AllWindows(); len(wins) > 0 {
				dialog.ShowInformation("Setup Complete", successMsg, wins[0])
			}

			a.fyneApp.SendNotification(fyne.NewNotification("Adder Started",
				"Now monitoring "+plan.Filter.Template))
		})
	}()
}

// Config returns a consistent snapshot of the current tray configuration.
// It is safe to call from any goroutine.
func (a *App) Config() TrayConfig {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.config
}

func (a *App) failTask(msg string, err error, ctrl *wizard.WizardController) {
	slog.Error(msg, "error", err)
	fyne.Do(func() {
		if ctrl != nil {
			ctrl.EnableButtons()
		}
		if len(a.fyneApp.Driver().AllWindows()) > 0 {
			dialog.ShowError(errors.New(msg+"\n\nError: "+err.Error()), a.fyneApp.Driver().AllWindows()[0])
		}
	})
}

// Run configures the system tray and starts the application loop.
func (a *App) Run() {
	a.setupTray()
	a.fyneApp.Run()
}

// setupTray configures the system tray menu and icon.
func (a *App) setupTray() {
	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		slog.Error("desktop features not supported on this platform")
		return
	}

	mStatus := fyne.NewMenuItem("Status: "+a.conn.status.Status().String(), nil)
	mStatus.Disabled = true

	a.mRecent = fyne.NewMenuItem("Recent Events", nil)
	a.mRecent.ChildMenu = fyne.NewMenu("")

	mClear := fyne.NewMenuItem("Clear History", func() {
		fyne.Do(func() {
			a.uiMu.Lock()
			defer a.uiMu.Unlock()
			a.recentEvents = nil
			a.mRecent.ChildMenu = fyne.NewMenu("Recent")
			if a.mMenu != nil {
				a.mMenu.Refresh()
			}
		})
	})

	mStart := fyne.NewMenuItem("Start", func() {
		a.intentionalStop.Store(false)
		if err := setup.StartService(); err != nil {
			slog.Error("failed to start service", "error", err)
		}
		if err := a.conn.Connect(); err != nil {
			slog.Error("failed to connect", "error", err)
		}
	})
	mStop := fyne.NewMenuItem("Stop", func() {
		a.intentionalStop.Store(true)
		a.conn.Disconnect()
		if err := setup.StopService(); err != nil {
			slog.Error("failed to stop service", "error", err)
		}
	})
	mRestart := fyne.NewMenuItem("Restart", func() {
		a.intentionalStop.Store(false)
		a.conn.Disconnect()
		_ = setup.StopService()
		if err := setup.StartService(); err != nil {
			slog.Error("failed to start service", "error", err)
		}
		_ = a.conn.Connect()
	})

	mReconfigure := fyne.NewMenuItem("Reconfigure...", func() {
		plan, err := a.reconfigurePlan()
		if err != nil {
			slog.Error("failed to load engine config for reconfigure",
				"error", err)
			return
		}

		wizard.ShowWizard(&plan, a.onWizardFinish)
	})

	mShowConfig := fyne.NewMenuItem("Show Config Folder", func() {
		openFolder(ConfigDir())
	})
	mShowLogs := fyne.NewMenuItem("Show Logs", func() {
		openFolder(LogDir())
	})

	mAbout := fyne.NewMenuItem("About", func() {
		openURL("https://github.com/blinklabs-io/adder")
	})
	mQuit := fyne.NewMenuItem("Quit", func() {
		a.fyneApp.Quit()
	})

	menu := fyne.NewMenu("Adder",
		mStatus,
		fyne.NewMenuItemSeparator(),
		a.mRecent,
		mClear,
		fyne.NewMenuItemSeparator(),
		mStart,
		mStop,
		mRestart,
		mReconfigure,
		fyne.NewMenuItemSeparator(),
		mShowConfig,
		mShowLogs,
		fyne.NewMenuItemSeparator(),
		mAbout,
		mQuit,
	)
	a.mMenu = menu

	desk.SetSystemTrayMenu(menu)

	// Update status menu item when connection status changes.
	//
	// NOTE: StatusTracker.Set spawns one goroutine per state transition, so
	// rapid back-to-back transitions can still deliver observer calls
	// out-of-order (last-write-wins). Fixing delivery ordering would require
	// a serialising channel in StatusTracker — out of scope here.
	a.conn.status.OnChange(func(s Status) {
		icon := GetStatusIcon(s)
		slog.Info("tray status changed",
			"status", s.String(),
			"icon_name", icon.Name())

		// Sleep outside fyne.Do so the UI thread is never blocked while waiting
		// for the OS to settle between rapid icon transitions.
		time.Sleep(250 * time.Millisecond)
		fyne.Do(func() {
			a.uiMu.Lock()
			defer a.uiMu.Unlock()
			mStatus.Label = "Status: " + s.String()
			menu.Refresh()

			desk.SetSystemTrayIcon(icon)

			// Notify on connection issues if enabled
			cfg := a.Config()
			if cfg.NotifyPrefs[setup.NotifyPrefConnectionIssues] {
				if s == StatusError {
					a.fyneApp.SendNotification(fyne.NewNotification(
						"Adder Error",
						"A critical connection error occurred.",
					))
				} else if s == StatusStopped && !a.intentionalStop.Load() {
					a.fyneApp.SendNotification(fyne.NewNotification(
						"Adder Connection",
						"Lost connection to sidecar API. Reconnecting...",
					))
				}
			}
		})
	})

	// Bulletproof Startup Watchdog:
	// This goroutine periodically re-asserts the tray icon for the first 10
	// seconds of application life. It addresses macOS-specific rendering
	// quirks where rapid status transitions (e.g., Starting -> Connected)
	// can cause the OS to 'miss' an icon update, leaving it stuck in a gray
	// state. This ensures a consistent, high-quality first-user experience.
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(10 * time.Second)

		for {
			select {
			case <-ticker.C:
				fyne.Do(func() {
					a.uiMu.Lock()
					s := a.conn.status.Status()
					desk.SetSystemTrayIcon(GetStatusIcon(s))
					a.uiMu.Unlock()
				})
			case <-a.quitChan:
				return
			case <-timeout:
				slog.Debug("startup icon watchdog finished")
				return
			}
		}
	}()

	// Event processing and notification dispatcher
	go func() {
		var eventCount int
		for {
			select {
			case <-a.quitChan:
				return
			case evt, ok := <-a.conn.Events():
				if !ok {
					return
				}
				eventCount++

				// 1. Log the event arrival for debugging
				slog.Debug("received event from sidecar",
					"type", evt.Type,
					"count", eventCount)

				// 2. Dispatch notification if enabled in preferences
				a.dispatchNotification(evt)
			}
		}
	}()

	slog.Info("starting adder-tray")

	if a.Config().AutoStart {
		// Ensure engine is running if configured
		status, _ := setup.ServiceStatusCheck()
		if status == setup.ServiceRegistered {
			_ = setup.StartService()
		}

		if err := a.conn.Connect(); err != nil {
			slog.Error(
				"failed to auto-connect to adder",
				"error", err,
			)
		}
	}
}

// Shutdown requests a graceful shutdown of the tray application.
func (a *App) Shutdown() {
	close(a.quitChan)
	a.fyneApp.Quit()
}

func (a *App) reconfigurePlan() (setup.SetupPlan, error) {
	cfg := a.Config()
	engineCfg, err := a.runner.Store.LoadEngine(cfg.AdderConfig)
	if err != nil {
		return setup.SetupPlan{}, err
	}
	return setup.SetupPlanFromEngineConfig(engineCfg, cfg), nil
}

func getEmojiForType(evtType string) string {
	switch evtType {
	case "input.block":
		return "🧱"
	case "input.transaction":
		return "💸"
	case "input.governance":
		return "🗳️"
	case "input.rollback":
		return "🔄"
	default:
		return "❓"
	}
}

func (a *App) addRecentEvent(evt event.Event) {
	fyne.Do(func() {
		a.uiMu.Lock()
		defer a.uiMu.Unlock()
		// Keep last 10 events (LIFO order for "Recent" display)
		a.recentEvents = append([]event.Event{evt}, a.recentEvents...)
		if len(a.recentEvents) > 10 {
			a.recentEvents = a.recentEvents[:10]
		}

		slog.Debug("updating recent events menu", "count", len(a.recentEvents))

		// Update the child menu
		items := make([]*fyne.MenuItem, 0, len(a.recentEvents))
		for _, e := range a.recentEvents {
			eventTime := e.Timestamp.Format("15:04:05")
			if e.Timestamp.IsZero() {
				eventTime = time.Now().Format("15:04:05")
			}
			emoji := getEmojiForType(e.Type)
			cleanType := strings.TrimPrefix(e.Type, "input.")
			label := fmt.Sprintf("%s %s (%s)", emoji, cleanType, eventTime)

			// Create action to "Show" the event in an explorer
			hash := ""
			if payload, ok := e.Payload.(map[string]any); ok {
				if h, ok := payload["blockHash"].(string); ok {
					hash = h
				} else if h, ok := payload["transactionHash"].(string); ok {
					hash = h
				}
			}

			item := fyne.NewMenuItem(label, func() {
				if hash != "" {
					url := getExplorerURL(e, hash)
					openURL(url)
				}
			})
			items = append(items, item)
		}

		a.mRecent.ChildMenu = fyne.NewMenu("Recent", items...)
		if a.mMenu != nil {
			a.mMenu.Refresh()
		}
	})
}

func getExplorerURL(e event.Event, hash string) string {
	baseURL := "https://cexplorer.io"
	// Inspect context for network info
	if ctx, ok := e.Context.(map[string]any); ok {
		if magic, ok := ctx["networkMagic"].(float64); ok {
			switch uint32(magic) {
			case 1:
				baseURL = "https://preprod.cexplorer.io"
			case 2:
				baseURL = "https://preview.cexplorer.io"
			}
		}
	}

	if e.Type == "input.transaction" {
		return fmt.Sprintf("%s/tx/%s", baseURL, hash)
	}
	return fmt.Sprintf("%s/block/%s", baseURL, hash)
}

// openFolder opens the given directory in the platform file manager.
func openFolder(dir string) {
	if dir == "" {
		slog.Error("cannot open empty directory path")
		return
	}

	// Ensure directory exists so 'open' doesn't fail or open script editor
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error("failed to create directory before opening", "dir", dir, "error", err)
	}

	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "explorer"
	default:
		cmd = "xdg-open"
	}
	slog.Debug("opening folder", "cmd", cmd, "dir", dir)
	p := exec.Command(cmd, dir) //nolint:gosec // directory path from internal config
	if err := p.Start(); err != nil {
		slog.Error("failed to open folder", "dir", dir, "error", err)
		return
	}
	_ = p.Process.Release()
}

// openURL opens the given URL in the default browser.
func openURL(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", "", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	p := exec.Command(cmd, args...) //nolint:gosec // hardcoded URL
	if err := p.Start(); err != nil {
		slog.Error("failed to open URL", "url", url, "error", err)
		return
	}
	_ = p.Process.Release()
}

func (a *App) dispatchNotification(evt event.Event) {
	// Always update history
	a.addRecentEvent(evt)

	var title, msg string
	var prefKey string

	emoji := getEmojiForType(evt.Type)

	// TODO(future): Replace this hardcoded logic with a proper Template Engine
	// to support more informative notifications (e.g., amount, addresses).
	switch evt.Type {
	case "input.block":
		prefKey = setup.NotifyPrefBlocksMinted
		title = emoji + " New Block"
		msg = "A new block has been minted."
		if payload, ok := evt.Payload.(map[string]any); ok {
			if hash, ok := payload["blockHash"].(string); ok {
				msg = "Block Hash: " + hash
			}
		}

	case "input.transaction":
		prefKey = setup.NotifyPrefIncomingTx
		title = emoji + " New Transaction"
		msg = "A new transaction was detected."
		if payload, ok := evt.Payload.(map[string]any); ok {
			if hash, ok := payload["transactionHash"].(string); ok {
				msg = "Hash: " + hash
			}
		}

	case "input.governance":
		prefKey = setup.NotifyPrefVotesCast
		title = emoji + " Governance Action"
		msg = "A new governance action was detected."

	case "input.rollback":
		// Rollbacks don't have a specific pref toggle, usually always notify
		title = emoji + " Chain Rollback"
		msg = "A chain rollback was detected."
		prefKey = setup.NotifyPrefBlocksMinted // Group with block alerts
	}

	if title != "" && a.Config().NotifyPrefs[prefKey] {
		// Log the exact dispatch for debugging
		slog.Info("dispatching native notification",
			"title", title,
			"msg", msg)

		// Use Fyne native notification (fixes Script Editor bug)
		a.fyneApp.SendNotification(fyne.NewNotification(title, msg))
	}
}
