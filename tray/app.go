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
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/explorer"
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
	recentEvents []recentAlert
	mRecent      *fyne.MenuItem
	mMenu        *fyne.Menu
	// recentKeys dedups engine events replayed by the API ring buffer on
	// every reconnect (each Apply restart triggers a reconnect). Bounded
	// FIFO (recentKeyOrder evicts oldest) sized to the ring buffer so a
	// full replay cannot re-add a still-tracked event. Guarded by uiMu.
	recentKeys     map[string]struct{}
	recentKeyOrder []string

	// intentionalStop records whether the user deliberately stopped the
	// service, so a Stopped status is not reported as a lost connection.
	// Written from menu handlers and read from the status observer
	// goroutine, hence atomic.
	intentionalStop atomic.Bool
	// applyDeadline suppresses the transient connection alerts an Apply
	// (wizard finish / reconfigure / notification-rules edit) emits when
	// it restarts the engine. It holds a UnixNano deadline (0 = not
	// applying): alerts are suppressed until the connection settles
	// (StatusConnected/StatusError clears it) OR the deadline passes —
	// the deadline guarantees a genuine, persistent loss still surfaces
	// if the engine never reconnects after a restart. Written from
	// applyPlan, read from the status observer goroutine, hence atomic.
	applyDeadline atomic.Int64
	quitChan      chan struct{}
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
		slog.Warn(
			"failed to get user cache dir, falling back to temp",
			"error",
			err,
		)
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
		slog.Error(
			"failed to write block icon",
			"path",
			blockPath,
			"error",
			err,
		)
	}
	if err := os.WriteFile(govPath, assets.GetGovernanceIcon(128).Content(), 0o600); err != nil {
		slog.Error(
			"failed to write governance icon",
			"path",
			govPath,
			"error",
			err,
		)
	}
	if err := os.WriteFile(txPath, assets.GetTransactionIcon(128).Content(), 0o600); err != nil {
		slog.Error(
			"failed to write transaction icon",
			"path",
			txPath,
			"error",
			err,
		)
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

	enable := func() {
		if ctrl != nil {
			ctrl.EnableButtons()
		}
	}
	var parent fyne.Window
	if ctrl != nil {
		parent = ctrl.Window()
	}

	go func() {
		result, err := a.applyPlan(ctx, plan)
		if err != nil {
			a.failTask("Failed to apply configuration", err, parent, enable)
			return
		}
		// Keep the wizard open with re-enabled inputs so the warning
		// dialog (a modal child of the wizard) has a stable parent.
		if a.handleSoftErrors(result, parent, enable) {
			return
		}

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

// applyPlan persists a SetupPlan and hot-reloads the running tray:
// runs SetupRunner.Apply, refreshes the cached tray config from the
// returned snapshot, and swaps the engine's rule set + rate-limit.
// err != nil signals a pre-save failure (load + save steps); post-save
// problems are non-fatal soft errors on ApplyResult, so when err != nil
// nothing was persisted and there is nothing to reconcile.
func (a *App) applyPlan(
	ctx context.Context, plan setup.SetupPlan,
) (setup.ApplyResult, error) {
	// Suppress the transient connection alerts the engine restart inside
	// Apply would otherwise emit. runner.Apply reconnects synchronously,
	// but the status observer fires asynchronously, so a plain
	// clear-on-return would race that delivery and leak phantom alerts.
	// Instead arm a deadline: the observer clears it on the settling
	// StatusConnected/StatusError, and the deadline bounds suppression so
	// a genuine, persistent loss still surfaces if the engine never
	// reconnects after the restart.
	a.applyDeadline.Store(time.Now().Add(applyGrace).UnixNano())

	result, err := a.runner.Apply(ctx, plan)
	if err != nil {
		a.applyDeadline.Store(0)
		return result, err
	}
	if ctx.Err() != nil {
		a.applyDeadline.Store(0)
	}
	a.configMu.Lock()
	a.config = result.TrayConfig
	a.configMu.Unlock()
	// Rate limit comes from the persisted snapshot; rules come from
	// the input plan (the runner deep-copies plan.Filter+plan.Notify
	// into trayCfg, so they describe the same state — adding
	// canonicalization to SaveTrayAtomic would require deriving a
	// SetupPlan from the snapshot here for RulesFromPlan to keep up).
	// notifyEngine is nil on first run (setupTray runs after
	// onWizardFinish) and picks up rules + limits at construction.
	if eng := a.notifyEngine.Load(); eng != nil {
		eng.SetRules(notifications.RulesFromPlan(plan))
		limit, window := result.TrayConfig.ResolvedNotifyRate()
		eng.SetRateLimit(limit, window)
	}
	return result, nil
}

// Config returns a consistent snapshot of the current tray configuration.
// It is safe to call from any goroutine.
func (a *App) Config() TrayConfig {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.config
}

// surfaceSoftApplyErrors shows a warning dialog summarising the
// non-fatal errors Apply returned. The config IS saved; the source
// surface stays open so the user can read the warning and decide
// whether to retry or close.
func (a *App) surfaceSoftApplyErrors(
	result setup.ApplyResult, parent fyne.Window,
) {
	// Each paragraph is gated on a per-error predicate so the
	// composition stays data-driven: adding a fourth soft category
	// is one entry, not another open-coded if + msg-glue block. The
	// ReconnectErr predicate suppresses it when Binary/Restart fired
	// — those root causes are already explained above and they
	// directly imply Reconnect will fail; repeating "service may be
	// starting up, try again" would mislead the user (nothing was
	// asked to start).
	paragraphs := []struct {
		when bool
		body string
	}{
		{
			when: result.BinaryFindErr != nil,
			body: "Could not find the adder binary: " +
				errString(result.BinaryFindErr) +
				"\nThe service was not (re)started; install " +
				"adder or start it manually.",
		},
		{
			when: result.ServiceRestartErr != nil,
			body: "Failed to (re)start the adder service: " +
				errString(result.ServiceRestartErr) +
				"\nThe running process may not reflect your new " +
				"configuration; restart adder manually.",
		},
		{
			when: result.ReconnectErr != nil &&
				result.BinaryFindErr == nil &&
				result.ServiceRestartErr == nil,
			body: "Adder API not reachable yet: " +
				errString(result.ReconnectErr) +
				"\nThe config is saved; the service may still be " +
				"starting up. Try again in a moment if status " +
				"does not recover.",
		},
	}
	var parts []string
	for _, p := range paragraphs {
		if p.when {
			parts = append(parts, p.body)
		}
	}
	if len(parts) == 0 {
		return
	}
	msg := strings.Join(parts, "\n\n")
	slog.Warn("setup completed with soft errors", "summary", msg)
	fyne.Do(func() {
		win := a.resolveDialogParent(parent)
		if win == nil {
			return
		}
		dialog.ShowInformation(
			"Setup Complete (with warnings)", msg, win)
	})
}

// resolveDialogParent returns the explicit parent window when given,
// else falls back to the first window the Fyne driver still tracks.
// Returns nil when no window is available (dialog drops silently —
// slog.* already records the underlying event).
func (a *App) resolveDialogParent(parent fyne.Window) fyne.Window {
	if parent != nil {
		return parent
	}
	if wins := a.fyneApp.Driver().AllWindows(); len(wins) > 0 {
		return wins[0]
	}
	return nil
}

// handleSoftErrors surfaces the warning dialog and re-enables the
// caller's frozen inputs when result has soft errors; the source
// surface stays open so the dialog has a stable parent and the user
// can retry. Returns true when soft errors fired (caller skips its
// success path), false otherwise.
func (a *App) handleSoftErrors(
	result setup.ApplyResult, parent fyne.Window, enable func(),
) bool {
	if !result.HasSoftErrors() {
		return false
	}
	a.surfaceSoftApplyErrors(result, parent)
	if enable != nil {
		fyne.Do(enable)
	}
	return true
}

// errString returns err.Error() or "" for nil — keeps surfaceSoft's
// data table free of per-row nil checks.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// failTask logs the error, re-enables the caller's frozen inputs via
// onFail, and shows an error dialog on the first available window.
// AllWindows()[0] is best-effort: a concurrently-closing source
// surface lands the dialog on whichever window Fyne still tracks;
// when none remains the dialog drops silently (slog.Error already
// records the failure).
func (a *App) failTask(
	msg string, err error, parent fyne.Window, onFail func(),
) {
	slog.Error(msg, "error", err)
	fyne.Do(func() {
		if onFail != nil {
			onFail()
		}
		win := a.resolveDialogParent(parent)
		if win == nil {
			return
		}
		dialog.ShowError(
			errors.New(msg+"\n\nError: "+err.Error()), win)
	})
}

// onRulesApply is the apply callback for the rule editor: persists
// via applyPlan, surfaces hard errors via failTask and soft errors
// via handleSoftErrors; on full success closes the editor and emits a
// tray notification.
func (a *App) onRulesApply(
	ctx context.Context,
	plan setup.SetupPlan,
	ed *wizard.RulesEditor,
) {
	slog.Info("notification rules edited, applying changes")

	enable := func() {
		if ed != nil {
			ed.EnableButtons()
		}
	}
	var parent fyne.Window
	if ed != nil {
		parent = ed.Window()
	}

	go func() {
		result, err := a.applyPlan(ctx, plan)
		if err != nil {
			a.failTask("Failed to apply notification rules",
				err, parent, enable)
			return
		}
		// Soft errors: shared with onWizardFinish via handleSoftErrors
		// — keeps the editor open with re-enabled inputs so the
		// warning dialog (a modal child of the editor) has a stable
		// parent.
		if a.handleSoftErrors(result, parent, enable) {
			return
		}

		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			if ed != nil {
				ed.Close()
			}
			a.fyneApp.SendNotification(fyne.NewNotification(
				"Adder Rules Updated",
				"Notification rules applied.",
			))
		})
	}()
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
			a.recentKeys = nil
			a.recentKeyOrder = nil
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

	mRules := fyne.NewMenuItem("Notification Rules...", func() {
		plan, err := a.reconfigurePlan()
		if err != nil {
			slog.Error("failed to load engine config for rules editor",
				"error", err)
			return
		}

		wizard.ShowRulesEditor(plan, a.onRulesApply)
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
		mRules,
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
		a.addRecentAlert,
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
	var hasConnected atomic.Bool
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

		// During an Apply the engine restart cycles the connection
		// (stopped → connected). Suppress the phantom Lost/Reconnected
		// alerts, clearing the deadline once the connection settles.
		if dl := a.applyDeadline.Load(); dl != 0 {
			suppress, clear := applySuppress(dl, time.Now().UnixNano(), s)
			if clear {
				a.applyDeadline.Store(0)
			}
			if suppress {
				// Mark a suppressed connect as seen so the first genuine
				// reconnect after this Apply still notifies (otherwise it
				// looks like the first-ever connect and is skipped).
				if s == StatusConnected {
					hasConnected.Store(true)
				}
				return
			}
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
			if hasConnected.Swap(true) {
				eng.NotifyConnection(
					"Adder Connection",
					"Reconnected to node.")
			}
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

	// Bring the engine to a monitorable state, then connect
	// unconditionally so the tray always reflects the real engine state
	// (the EventClient retries with backoff, so a down engine shows
	// "reconnecting" rather than a permanent gray "stopped").
	//
	// If a config exists, assert the registration on every tray launch
	// and restart only when the rendered command or config contents
	// changed. This is the same lifecycle guarantee used by wizard
	// Apply/Reconfigure, and repairs a deleted or stale login-startup
	// artifact without requiring the user to open the wizard.
	if ConfigExists() {
		if err := a.ensureConfiguredService(); err != nil {
			slog.Error("failed to ensure adder service", "error", err)
			a.fyneApp.SendNotification(fyne.NewNotification(
				"Adder setup incomplete",
				"Could not ensure the adder service. Open the tray "+
					"menu and choose Reconfigure… to finish setup.",
			))
		}
	}

	if err := a.conn.Connect(); err != nil {
		slog.Error("failed to connect to adder", "error", err)
	}
}

// ensureConfiguredService asserts registration and runtime state for the
// saved engine config using the same two-step lifecycle as wizard Apply.
func (a *App) ensureConfiguredService() error {
	if a.runner == nil || a.runner.Finder == nil || a.runner.Service == nil {
		return errors.New("service lifecycle dependencies are not configured")
	}
	binPath, err := a.runner.Finder.Find()
	if err != nil {
		return fmt.Errorf("finding adder binary: %w", err)
	}
	cfgPath := a.Config().AdderConfig
	if cfgPath == "" {
		cfgPath = filepath.Join(setup.ConfigDir(), "config.yaml")
	}
	if err := a.runner.Service.EnsureRegistered(binPath, cfgPath); err != nil {
		return fmt.Errorf("registering service: %w", err)
	}
	if err := a.runner.Service.RestartIfConfigChanged(binPath, cfgPath); err != nil {
		return fmt.Errorf("ensuring service is running: %w", err)
	}
	return nil
}

// Shutdown gracefully tears down the tray. Ordering: close quitChan,
// join the producer, stop the engine, then quit fyne. Idempotent via
// sync.Once.
func (a *App) Shutdown() {
	a.shutdownOnce.Do(func() {
		close(a.quitChan)
		if a.conn != nil {
			a.conn.Disconnect()
		}
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

// recentEventKeyCap bounds the dedup set. Sized to the API ring buffer
// (defaultRingSize) so a full history replay on reconnect cannot re-add
// a still-tracked event.
const recentEventKeyCap = 128

// applyGrace bounds how long an Apply suppresses connection alerts. The
// restart churn (stop → reconnect) settles well under this; if the
// engine never reconnects within it, suppression lapses so a genuine
// persistent loss still surfaces. Var (not const) so tests can shrink it.
var applyGrace = 10 * time.Second

// applySuppress decides, for a connection-status transition, whether to
// suppress its notification and whether to clear the Apply deadline. It
// is pure so the lifecycle is unit-testable without timers:
//
//   - deadline == 0 → not applying: never suppress, nothing to clear.
//   - now >= deadline → suppression lapsed: surface the alert and clear
//     the stale deadline (covers an engine that never reconnected).
//   - StatusError → surface the error but clear (a failed Apply settled).
//   - StatusConnected → suppress this restart connect and clear (settled).
//   - anything else while in-flight → suppress (transient restart churn).
func applySuppress(
	deadlineNano, nowNano int64, s Status,
) (suppress, clear bool) {
	if deadlineNano == 0 {
		return false, false
	}
	if nowNano >= deadlineNano {
		return false, true
	}
	if s == StatusError {
		return false, true
	}
	if s == StatusConnected {
		return true, true
	}
	return true, false
}

// recentLabel builds a recent-events menu label. The notification title
// already carries an emoji for chain events (rules.go), so prepend the
// type emoji only when the title has none — otherwise it doubles up
// (e.g. "🔄 🔄 Chain Rollback"). Connection alerts start with a letter
// and get the getEmojiForType fallback.
func recentLabel(title, evtType, eventTime string) string {
	if titleHasLeadingEmoji(title) {
		return fmt.Sprintf("%s (%s)", title, eventTime)
	}
	return fmt.Sprintf("%s %s (%s)",
		getEmojiForType(evtType), title, eventTime)
}

// titleHasLeadingEmoji reports whether the first rune is a symbol/emoji
// rather than a letter, digit or space — i.e. the title already begins
// with its own icon.
func titleHasLeadingEmoji(title string) bool {
	r, sz := utf8.DecodeRuneInString(title)
	if sz == 0 || r == utf8.RuneError {
		return false
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r)
}

// recentEventKey is a stable identity for dedup. explorerHash gives a
// tx/block hash when present; Type+timestamp disambiguates events
// without one (e.g. rollbacks).
func recentEventKey(e event.Event) string {
	return e.Type + "|" + explorerHash(e) + "|" +
		e.Timestamp.Format(time.RFC3339Nano)
}

// recentEventSeen reports whether the event was already added. Caller
// must hold uiMu.
func (a *App) recentEventSeen(e event.Event) bool {
	_, ok := a.recentKeys[recentEventKey(e)]
	return ok
}

// markRecentEvent records the event key, evicting the oldest when the
// bounded set is full. Caller must hold uiMu.
func (a *App) markRecentEvent(e event.Event) {
	key := recentEventKey(e)
	if a.recentKeys == nil {
		a.recentKeys = make(map[string]struct{}, recentEventKeyCap)
	}
	a.recentKeys[key] = struct{}{}
	a.recentKeyOrder = append(a.recentKeyOrder, key)
	if len(a.recentKeyOrder) > recentEventKeyCap {
		oldest := a.recentKeyOrder[0]
		a.recentKeyOrder = a.recentKeyOrder[1:]
		delete(a.recentKeys, oldest)
	}
}

type recentAlert struct {
	Title     string
	Timestamp time.Time
	Event     event.Event
}

func (a *App) addRecentAlert(req notifications.Request) {
	title := req.Title
	if title == "" {
		title = "Adder"
	}
	ts := req.Event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	alert := recentAlert{
		Title:     title,
		Timestamp: ts,
		Event:     req.Event,
	}

	fyne.Do(func() {
		a.uiMu.Lock()
		defer a.uiMu.Unlock()

		// Drop events already shown: the API ring buffer replays its
		// history to every reconnecting client, so an Apply restart
		// would otherwise duplicate the same event. Connection alerts
		// (empty Type) are intentionally repeatable and skip dedup.
		if alert.Event.Type != "" {
			if a.recentEventSeen(alert.Event) {
				return
			}
			a.markRecentEvent(alert.Event)
		}

		// Keep the latest 10 matching alerts, newest first so the most recent
		// transaction or chain event is immediately visible at the top.
		a.recentEvents = append(a.recentEvents, recentAlert{})
		copy(a.recentEvents[1:], a.recentEvents[:len(a.recentEvents)-1])
		a.recentEvents[0] = alert
		if len(a.recentEvents) > 10 {
			a.recentEvents = a.recentEvents[:10]
		}

		slog.Debug("updating recent events menu", "count", len(a.recentEvents))

		// Update the child menu
		items := make([]*fyne.MenuItem, 0, len(a.recentEvents))
		for _, alert := range a.recentEvents {
			eventTime := alert.Timestamp.Format("15:04:05")
			label := recentLabel(alert.Title, alert.Event.Type, eventTime)

			// Create action to "Show" the event in an explorer.
			// Pick the hash by event type: a transaction's/governance
			// action's hash lives in Context (transactionHash), a
			// block's in Payload (blockHash). Both tx and gov payloads
			// also carry blockHash, so reading the payload first would
			// mislink them to the block.
			hash := explorerHash(alert.Event)

			item := fyne.NewMenuItem(label, func() {
				if hash != "" {
					url := getExplorerURL(alert.Event, hash)
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
	// The event carries its own network magic in Context, so a link always
	// resolves on the chain the event came from (JSON numbers decode as float64).
	var magic uint32
	if ctx, ok := e.Context.(map[string]any); ok {
		if m, ok := ctx["networkMagic"].(float64); ok {
			magic = uint32(m)
		}
	}
	baseURL := explorer.BaseURL(magic)

	if e.Type == "input.transaction" || e.Type == "input.governance" {
		return fmt.Sprintf("%s/tx/%s", baseURL, hash)
	}
	return fmt.Sprintf("%s/block/%s", baseURL, hash)
}

// explorerHash returns the chain hash to link for an event, chosen by
// event type: transactions and governance actions are identified by
// their transactionHash in Context; blocks by blockHash in Payload.
// Reading the payload first would be wrong because tx and governance
// payloads also carry blockHash.
func explorerHash(e event.Event) string {
	switch e.Type {
	case "input.transaction", "input.governance":
		if ctx, ok := e.Context.(map[string]any); ok {
			if h, ok := ctx["transactionHash"].(string); ok {
				return h
			}
		}
	default:
		if p, ok := e.Payload.(map[string]any); ok {
			if h, ok := p["blockHash"].(string); ok {
				return h
			}
		}
	}
	return ""
}

// openFolder opens the given directory in the platform file manager.
func openFolder(dir string) {
	if dir == "" {
		slog.Error("cannot open empty directory path")
		return
	}

	// Ensure directory exists so 'open' doesn't fail or open script editor
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error(
			"failed to create directory before opening",
			"dir",
			dir,
			"error",
			err,
		)
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
	p := exec.Command(
		cmd,
		dir,
	) //nolint:gosec // directory path from internal config
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
