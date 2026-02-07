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
	"log/slog"

	"fyne.io/systray"
)

// App holds references to all major components of the tray
// application.
type App struct {
	config  TrayConfig
	process *ProcessManager
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

	a := &App{
		config: cfg,
		process: NewProcessManager(
			WithBinary(cfg.AdderBinary),
			WithConfigFile(cfg.AdderConfig),
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

	mStart := systray.AddMenuItem("Start", "Start adder")
	mStop := systray.AddMenuItem("Stop", "Stop adder")
	mRestart := systray.AddMenuItem(
		"Restart", "Restart adder",
	)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit adder-tray")

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
