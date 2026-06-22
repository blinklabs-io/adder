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
	"maps"
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
	"github.com/blinklabs-io/adder/tray/notifications"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/blinklabs-io/adder/tray/wizard"
)

// Notification rate-limit defaults live in tray/setup
// (DefaultNotifyRateLimit / DefaultNotifyRateWindow) and are
// overridable per-user via TrayConfig.NotifyRateLimit /
// NotifyRateWindow in adder-tray.yaml. Connection alerts bypass the
// limiter so a chain-event burst can never swallow a "lost
// connection" alert.

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
	// shutdownOnce guards Shutdown so multiple call sites (Quit menu,
	// signal handler, test cleanup) cannot double-close quitChan.
	shutdownOnce sync.Once
	// producerDone is closed by the event-forwarder goroutine on exit
	// so Shutdown can join the producer before stopping the engine —
	// keeps shutdown drops out of Stats() backpressure counters and
	// prevents producer fyne.Do calls against a torn-down driver. nil
	// before setupTray runs.
	producerDone chan struct{}

	// notifyEngine is the single notification dispatch path: chain
	// events + synthesized connection events in, rendered rate-limited
	// Requests out to the Dispatch goroutine. atomic.Pointer because
	// setupTray (main goroutine) writes it and several goroutines read
	// it (wizard-finish, status observer, producer, Stats ticker,
	// Shutdown).
	notifyEngine atomic.Pointer[notifications.Engine]
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
		result, err := a.runner.Apply(ctx, plan)
		if err != nil {
			a.failTask("Failed to apply configuration", err, ctrl)
			return
		}
		// Surface soft errors to the user — the config is saved, but
		// the service may not be running because the binary was not
		// found or the restart failed. Without this the wizard reports
		// "Setup Complete" and the user is left to discover the silent
		// failure via a later "API unreachable" error with no context.
		if result.HasSoftErrors() {
			a.surfaceSoftApplyErrors(result, ctrl)
		}
		if cfg, err := a.runner.Store.LoadTray(); err != nil {
			slog.Warn("failed to reload tray config after setup", "error", err)
		} else {
			a.configMu.Lock()
			a.config = cfg
			a.configMu.Unlock()
		}

		// Rebuild rules + push new rate-limit knobs so changes take
		// effect without a tray restart. On first run the engine
		// isn't yet constructed (setupTray runs later) — it picks up
		// both at construction time.
		if eng := a.notifyEngine.Load(); eng != nil {
			eng.SetRules(
				notifications.RulesFromPlan(a.notifyPlan()),
			)
			limit, window := a.Config().ResolvedNotifyRate()
			eng.SetRateLimit(limit, window)
		}

		// Success!
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			if ctrl != nil {
				ctrl.Close()
			}
			summary := setup.SummarizeFilter(plan.Filter)
			successMsg := fmt.Sprintf(
				"Successfully connected to Adder API at %s:%d.\n\nMonitoring: %s",
				plan.API.Address,
				plan.API.Port,
				summary,
			)

			// Show completion dialog if we have a window available
			if wins := a.fyneApp.Driver().AllWindows(); len(wins) > 0 {
				dialog.ShowInformation("Setup Complete", successMsg, wins[0])
			}

			a.fyneApp.SendNotification(fyne.NewNotification("Adder Started",
				"Now monitoring "+summary))
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

// surfaceSoftApplyErrors shows a warning dialog summarising the
// non-fatal errors Apply returned. The config IS saved at this point —
// we just need the user to know the running service may not reflect
// it so they can act (install adder, restart the service, etc.). The
// wizard is not failed; the user can still close it and proceed.
func (a *App) surfaceSoftApplyErrors(
	result setup.ApplyResult, ctrl *wizard.WizardController,
) {
	var msg string
	if result.BinaryFindErr != nil {
		msg += "Could not find the adder binary: " +
			result.BinaryFindErr.Error() +
			"\nThe service was not (re)started; install adder " +
			"or start it manually."
	}
	if result.ServiceRestartErr != nil {
		if msg != "" {
			msg += "\n\n"
		}
		msg += "Failed to (re)start the adder service: " +
			result.ServiceRestartErr.Error() +
			"\nThe running process may not reflect your new " +
			"configuration; restart adder manually."
	}
	slog.Warn("setup completed with soft errors", "summary", msg)
	fyne.Do(func() {
		if ctrl != nil {
			ctrl.EnableButtons()
		}
		if len(a.fyneApp.Driver().AllWindows()) > 0 {
			dialog.ShowInformation(
				"Setup Complete (with warnings)",
				msg,
				a.fyneApp.Driver().AllWindows()[0],
			)
		}
	})
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
		// Shutdown orchestrates the right teardown order before
		// fyneApp.Quit; calling fyneApp.Quit directly would leak the
		// producer goroutine and the engine.
		a.Shutdown()
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

	// Notification dispatch path: engine renders + rate-limits, the
	// Dispatch goroutine sends native notifications. Constructed and
	// stored on a.notifyEngine BEFORE OnChange is registered, because
	// OnChange fires synchronously once with the current status and
	// may call NotifyConnection immediately.
	engineEvents := make(chan event.Event, 64)
	rules := notifications.RulesFromPlan(a.notifyPlan())
	rateLimit, rateWindow := a.Config().ResolvedNotifyRate()
	slog.Info("notification rate limiter configured",
		"limit", rateLimit, "window", rateWindow)
	eng := notifications.NewEngine(
		engineEvents,
		rules,
		notifications.WithRateLimit(rateLimit, rateWindow),
	)
	a.notifyEngine.Store(eng)
	eng.Start()
	go notifications.Dispatch(
		eng.Requests(),
		a.fyneApp,
		eng.CurrentEpoch,
		eng.RecordDrop,
	)

	// Log non-zero drop deltas every 30s so suppressed notifications
	// are visible to operators. Terminates on quitChan close.
	go a.surfaceNotificationStats(eng)

	// Status observer: updates the status menu item and routes
	// connection alerts through the engine. initialFire suppresses
	// the synchronous first OnChange call (a fresh ConnectionManager
	// fires StatusStopped, which would otherwise read as "Lost
	// connection" before any connection attempt).
	//
	// NOTE: StatusTracker.Set spawns one goroutine per transition,
	// so rapid transitions can still deliver out-of-order.
	var initialFire atomic.Bool
	initialFire.Store(true)
	a.conn.status.OnChange(func(s Status) {
		icon := GetStatusIcon(s)
		slog.Info("tray status changed",
			"status", s.String(),
			"icon_name", icon.Name())

		// Sleep outside fyne.Do so the UI thread is never blocked
		// while waiting for the OS to settle between rapid icon
		// transitions.
		time.Sleep(250 * time.Millisecond)
		fyne.Do(func() {
			a.uiMu.Lock()
			defer a.uiMu.Unlock()
			mStatus.Label = "Status: " + s.String()
			menu.Refresh()

			desk.SetSystemTrayIcon(icon)
		})

		if suppressInitialFire(&initialFire, s) {
			return
		}

		// Connection-status notifications go through the engine: the
		// dispatcher is the single SendNotification path, the
		// "connection issues" pref gates them, and they bypass the
		// rate limiter. Severity-specific titles let the OS group
		// errors and reconnects separately in notification history.
		switch {
		case s == StatusError:
			eng.NotifyConnection(
				"Adder Error",
				"A critical connection error occurred.")
		case s == StatusStopped && !a.intentionalStop.Load():
			eng.NotifyConnection(
				"Adder Connection",
				"Lost connection to sidecar API. Reconnecting...")
		case s == StatusConnected:
			eng.NotifyConnection(
				"Adder Connection",
				"Reconnected to node.")
		}
	})

	// Startup watchdog: re-asserts the tray icon every 500ms for the
	// first 10s to work around macOS dropping icon updates during
	// rapid status transitions.
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

	// Event processing. conn.Events() has a single consumer (this
	// goroutine), so it both updates the recent-events menu and
	// forwards to the notification engine. engineEvents is closed on
	// exit so the engine and dispatcher terminate cleanly.
	// producerDone lets Shutdown join this goroutine before stopping
	// the engine.
	a.producerDone = make(chan struct{})
	go func() {
		defer close(a.producerDone)
		defer close(engineEvents)
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

				slog.Debug("received event from sidecar",
					"type", evt.Type,
					"count", eventCount)

				// Recent-events menu update (UI state, separate from
				// notification dispatch).
				a.addRecentEvent(evt)

				// Drop-rather-than-block on the engine queue so a slow
				// engine cannot stall event processing. Warn-level
				// because the drop is unrecoverable: the event never
				// reaches the rules engine and every rule that would
				// have matched is lost.
				select {
				case engineEvents <- evt:
				default:
					eng.RecordDrop()
					hash := chainIDFromPayload(evt.Payload)
					slog.Warn(
						"notification engine busy, event dropped",
						"type", evt.Type,
						"count", eventCount,
						"hash", hash)
				}
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

// Shutdown gracefully tears down the tray. Ordering: close quitChan,
// join the producer, stop the engine, then quit fyne. Idempotent via
// sync.Once.
func (a *App) Shutdown() {
	a.shutdownOnce.Do(func() {
		close(a.quitChan)
		if a.producerDone != nil {
			<-a.producerDone
		}
		if eng := a.notifyEngine.Load(); eng != nil {
			eng.Stop()
		}
		a.fyneApp.Quit()
	})
}

// surfaceNotificationStats periodically logs non-zero deltas in the
// engine's drop counter so operators have a single line to grep for
// "notifications suppressed" in production. Without this the
// Stats().Dropped counter is populated by emit, NotifyConnection,
// SetRules drain, dispatch stale-epoch, and producer-side
// backpressure paths, but never read by anything in the live tray.
// Exits when quitChan closes.
func (a *App) surfaceNotificationStats(eng *notifications.Engine) {
	const interval = 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastReported int64
	for {
		select {
		case <-a.quitChan:
			return
		case <-ticker.C:
			total := eng.Stats().Dropped
			delta := total - lastReported
			if delta <= 0 {
				continue
			}
			lastReported = total
			slog.Info(
				"notifications suppressed in last interval",
				"delta", delta,
				"total", total,
				"interval", interval)
		}
	}
}

// chainIDFromPayload returns the block or transaction hash from an
// event payload, or "" when the payload shape is unfamiliar.
func chainIDFromPayload(p any) string {
	m, ok := p.(map[string]any)
	if !ok {
		return ""
	}
	if h, ok := m["blockHash"].(string); ok && h != "" {
		return h
	}
	if h, ok := m["transactionHash"].(string); ok && h != "" {
		return h
	}
	return ""
}

// notifyPlan returns the SetupPlan used to build notification rules.
// On load failure it falls back to MonitorEverything so chain-event
// notifications still fire instead of being silently muted.
func (a *App) notifyPlan() setup.SetupPlan {
	fallback := a.fallbackPlan()
	if a.runner == nil || a.runner.Store == nil {
		return fallback
	}
	plan, err := a.reconfigurePlan()
	if err != nil {
		slog.Warn(
			"failed to load plan for notification rules; "+
				"using MonitorEverything fallback so chain "+
				"notifications still fire",
			"error", err,
		)
		return fallback
	}
	return plan
}

// fallbackPlan returns a MonitorEverything plan with a copy of the
// current tray notify prefs (copy so plan mutations don't leak into
// the persisted TrayConfig).
func (a *App) fallbackPlan() setup.SetupPlan {
	prefs := a.Config().NotifyPrefs
	notify := make(setup.NotificationPrefs, len(prefs))
	maps.Copy(notify, prefs)
	return setup.SetupPlan{
		Filter: setup.FilterConfig{MonitorEverything: true},
		Notify: notify,
	}
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

// suppressInitialFire decides whether to swallow an OnChange
// invocation as the synchronous "initial fire" that
// StatusTracker.OnChange always emits on registration. It swallows the
// first call ONLY when the observed status is the default
// StatusStopped — the tracker has never actually transitioned. A first
// fire with any other status (e.g. StatusError because an upstream
// auto-connect failed before OnChange was registered) is a real event
// the user must see; blanket-suppressing the first fire would silently
// hide it.
func suppressInitialFire(first *atomic.Bool, s Status) bool {
	return first.Swap(false) && s == StatusStopped
}
