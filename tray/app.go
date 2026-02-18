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
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"

	"fyne.io/systray"
)

const (
	defaultAPIAddress = "127.0.0.1"
	defaultAPIPort    = 8080
)

// App holds references to all major components of the tray
// application.
type App struct {
	config  TrayConfig
	process *ProcessManager
	status  *StatusTracker
}

// NewApp creates and initialises the tray application.
func NewApp() (*App, error) {
	cfg, err := LoadConfig()
	if err != nil {
		slog.Warn(
			"failed to load config, using defaults",
			"error", err,
		)
		cfg = DefaultConfig()
	}

	status := NewStatusTracker()

	a := &App{
		config: cfg,
		status: status,
		process: NewProcessManager(
			WithBinary(cfg.AdderBinary),
			WithConfigFile(cfg.AdderConfig),
			WithStatusTracker(status),
			WithAPIEndpoint(defaultAPIAddress, defaultAPIPort),
			WithAutoRestart(true),
		),
	}

	return a, nil
}

// Run starts the system tray and blocks until Quit is called.
func (a *App) Run() {
	systray.Run(a.onReady, a.onExit)
}

// onReady is called when the system tray is initialised. It
// configures the tray icon, menu, and starts adder if configured.
func (a *App) onReady() {
	systray.SetTitle("Adder")
	systray.SetTooltip("Adder - Cardano Event Streamer")

	// Status display (disabled — updated via observer)
	mStatus := systray.AddMenuItem(
		"Status: Stopped", "Current adder status",
	)
	mStatus.Disable()

	systray.AddSeparator()

	mStart := systray.AddMenuItem("Start", "Start adder")
	mStop := systray.AddMenuItem("Stop", "Stop adder")
	mRestart := systray.AddMenuItem(
		"Restart", "Restart adder",
	)

	systray.AddSeparator()

	mShowConfig := systray.AddMenuItem(
		"Show Config Folder", "Open configuration directory",
	)
	mShowLogs := systray.AddMenuItem(
		"Show Logs", "Open log directory",
	)

	systray.AddSeparator()

	mAbout := systray.AddMenuItem(
		"About Adder", "Show version information",
	)

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit adder-tray")

	// Update status menu item when status changes
	a.status.OnChange(func(s Status) {
		mStatus.SetTitle(fmt.Sprintf("Status: %s", s))
	})

	go func() {
		for {
			select {
			case <-mStart.ClickedCh:
				if err := a.process.Start(); err != nil {
					slog.Error(
						"failed to start adder",
						"error", err,
					)
				}
			case <-mStop.ClickedCh:
				if err := a.process.Stop(); err != nil {
					slog.Error(
						"failed to stop adder",
						"error", err,
					)
				}
			case <-mRestart.ClickedCh:
				if err := a.process.Restart(); err != nil {
					slog.Error(
						"failed to restart adder",
						"error", err,
					)
				}
			case <-mShowConfig.ClickedCh:
				openFolder(ConfigDir())
			case <-mShowLogs.ClickedCh:
				openFolder(LogDir())
			case <-mAbout.ClickedCh:
				slog.Info("Adder - Cardano Event Streamer")
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()

	slog.Info("starting adder-tray")

	if a.config.AutoStart {
		if err := a.process.Start(); err != nil {
			slog.Error(
				"failed to auto-start adder",
				"error", err,
			)
		}
	}
}

// onExit is called when the system tray is shutting down.
func (a *App) onExit() {
	slog.Info("shutting down adder-tray")

	if a.process.IsRunning() {
		if err := a.process.Stop(); err != nil {
			slog.Error(
				"error stopping adder during shutdown",
				"error", err,
			)
		}
	}
}

// Shutdown requests a graceful shutdown of the tray application
// and its managed adder process.
func (a *App) Shutdown() {
	systray.Quit()
}

// openFolder opens the given directory in the platform's file
// manager.
func openFolder(dir string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "explorer"
	default:
		cmd = "xdg-open"
	}

	if err := exec.Command(cmd, dir).Start(); err != nil { //nolint:gosec // command selected by platform, dir from internal paths
		slog.Error(
			"failed to open folder",
			"dir", dir,
			"error", err,
		)
	}
}
