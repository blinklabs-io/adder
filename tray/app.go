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
	"image/color"
	"log/slog"
	"os/exec"
	"runtime"
	"strconv"
	"time"

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

	mStart := systray.AddMenuItem("Start", "Start adder service")
	mStop := systray.AddMenuItem("Stop", "Stop adder service")
	mRestart := systray.AddMenuItem("Restart", "Restart adder service")
	systray.AddSeparator()

	mShowConfig := systray.AddMenuItem(
		"Show Config Folder", "Open the config directory",
	)
	mShowLogs := systray.AddMenuItem(
		"Show Logs", "Open the log directory",
	)
	systray.AddSeparator()

	mAbout := systray.AddMenuItem("About", "About Adder")
	mQuit := systray.AddMenuItem("Quit", "Quit adder-tray")

	// Update status menu item when connection status changes
	a.conn.status.OnChange(func(s Status) {
		mStatus.SetTitle("Status: " + s.String())

		// Update icon color based on status
		switch s {
		case StatusConnected:
			systray.SetIcon(generateIcon(color.RGBA{0, 255, 0, 255})) // Green
		case StatusReconnecting, StatusStarting:
			systray.SetIcon(generateIcon(color.RGBA{255, 255, 0, 255})) // Yellow
		case StatusStopped, StatusError:
			systray.SetIcon(generateIcon(color.RGBA{255, 0, 0, 255})) // Red
		default:
			systray.SetIcon(generateIcon(color.RGBA{128, 128, 128, 255})) // Gray
		}
	})

	// Initial icon state
	systray.SetIcon(generateIcon(color.RGBA{128, 128, 128, 255}))

	// Event tracking for tooltip
	go func() {
		var eventCount int
		var lastEvent time.Time
		for range a.conn.Events() {
			eventCount++
			lastEvent = time.Now()

			tooltip := "Adder - Cardano Event Streamer"
			if eventCount > 0 {
				tooltip += "\nEvents: " + strconv.Itoa(eventCount) + "\nLast: " + lastEvent.Format("15:04:05")
			}
			systray.SetTooltip(tooltip)
		}
	}()

	go func() {
		for {
			select {
			case <-mStart.ClickedCh:
				if err := StartService(); err != nil {
					slog.Error("failed to start service", "error", err)
				}
				if err := a.conn.Connect(); err != nil {
					slog.Error(
						"failed to connect",
						"error", err,
					)
				}
			case <-mStop.ClickedCh:
				a.conn.Disconnect()
				if err := StopService(); err != nil {
					slog.Error("failed to stop service", "error", err)
				}
			case <-mRestart.ClickedCh:
				a.conn.Disconnect()
				if err := StopService(); err != nil {
					slog.Error("failed to stop service", "error", err)
				}
				if err := StartService(); err != nil {
					slog.Error("failed to start service", "error", err)
				}
				if err := a.conn.Connect(); err != nil {
					slog.Error(
						"failed to connect",
						"error", err,
					)
				}
			case <-mAbout.ClickedCh:
				openURL("https://github.com/blinklabs-io/adder")
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
