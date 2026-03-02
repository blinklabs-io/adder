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
	"os/exec"
	"runtime"

	"fyne.io/systray"
)

// App holds references to all major components of the tray
// application.
type App struct {
	config TrayConfig
	conn   *ConnectionManager
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
		conn: NewConnectionManager(
			WithConnectionAddress(cfg.APIAddress),
			WithConnectionPort(cfg.APIPort),
		),
	}

	return a, nil
}

// Run starts the system tray and blocks until Quit is called.
func (a *App) Run() {
	systray.Run(a.onReady, a.onExit)
}

// onReady is called when the system tray is initialised. It
// configures the tray icon, menu, and connects to adder if
// configured.
func (a *App) onReady() {
	systray.SetTitle("Adder")
	systray.SetTooltip("Adder - Cardano Event Streamer")

	mStatus := systray.AddMenuItem(
		"Status: "+a.conn.status.Status().String(), "",
	)
	mStatus.Disable()
	systray.AddSeparator()

	mConnect := systray.AddMenuItem("Connect", "Connect to adder")
	mDisconnect := systray.AddMenuItem(
		"Disconnect", "Disconnect from adder",
	)
	mReconnect := systray.AddMenuItem(
		"Reconnect", "Reconnect to adder",
	)
	systray.AddSeparator()

	mShowConfig := systray.AddMenuItem(
		"Show Config Folder", "Open the config directory",
	)
	mShowLogs := systray.AddMenuItem(
		"Show Logs", "Open the log directory",
	)
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit adder-tray")

	// Update status menu item when connection status changes
	a.conn.status.OnChange(func(s Status) {
		mStatus.SetTitle("Status: " + s.String())
	})

	go func() {
		for {
			select {
			case <-mConnect.ClickedCh:
				if err := a.conn.Connect(); err != nil {
					slog.Error(
						"failed to connect",
						"error", err,
					)
				}
			case <-mDisconnect.ClickedCh:
				a.conn.Disconnect()
			case <-mReconnect.ClickedCh:
				if err := a.conn.Reconnect(); err != nil {
					slog.Error(
						"failed to reconnect",
						"error", err,
					)
				}
			case <-mShowConfig.ClickedCh:
				openFolder(ConfigDir())
			case <-mShowLogs.ClickedCh:
				openFolder(LogDir())
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()

	slog.Info("starting adder-tray")

	if a.config.AutoStart {
		if err := a.conn.Connect(); err != nil {
			slog.Error(
				"failed to auto-connect to adder",
				"error", err,
			)
		}
	}
}

// onExit is called when the system tray is shutting down. It
// disconnects the WS client but does NOT stop the adder service.
func (a *App) onExit() {
	slog.Info("shutting down adder-tray")
	a.conn.Disconnect()
}

// Shutdown requests a graceful shutdown of the tray application.
func (a *App) Shutdown() {
	systray.Quit()
}

// openFolder opens the given directory in the platform file manager.
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
	p := exec.Command(cmd, dir) //nolint:gosec // directory path from internal config
	if err := p.Start(); err != nil {
		slog.Error("failed to open folder", "dir", dir, "error", err)
		return
	}
	_ = p.Process.Release()
}
