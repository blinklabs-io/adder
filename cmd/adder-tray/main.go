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

package main

import (
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"

	"fyne.io/fyne/v2/app"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/ui/assets"
	"github.com/blinklabs-io/adder/tray"
	"github.com/blinklabs-io/adder/tray/setup"
)

func main() {
	// On Windows the MSI bundles Mesa's software OpenGL (opengl32.dll +
	// libgallium_wgl.dll) next to the exe so the Fyne GUI renders on VMs /
	// headless / RDP hosts that lack a hardware OpenGL driver (e.g.
	// VirtualBox). Pin the Gallium driver to llvmpipe so Mesa does not try the
	// D3D12 (dozen) path, which needs dxil.dll we do not ship. Respect an
	// explicit user override.
	if runtime.GOOS == "windows" {
		if _, ok := os.LookupEnv("GALLIUM_DRIVER"); !ok {
			_ = os.Setenv("GALLIUM_DRIVER", "llvmpipe")
		}
	}

	// Initialize logging
	cfg := config.GetConfig()
	if err := cfg.Load(""); err != nil {
		slog.Warn("failed to load environment config", "error", err)
	}

	// The tray is linked -H=windowsgui on Windows, so it has no console and
	// os.Stderr is discarded. Tee logs to a file so errors and crashes are
	// diagnosable; keep stderr for dev/console builds where it is visible.
	//
	// The log FILE is listed first, and deliberately so: io.MultiWriter stops
	// at the first writer that errors, and on Windows GUI os.Stderr.Write
	// fails (invalid handle). If stderr came first, the file — the whole point
	// on Windows — would never be written. File-first guarantees the file is
	// always written; a subsequent stderr error is harmless (slog discards
	// handler write errors).
	logOut := io.Writer(os.Stderr)
	if f := openTrayLogFile(); f != nil {
		logOut = io.MultiWriter(f, os.Stderr)
	}
	logging.ConfigureWithWriter(logOut)
	slog.SetDefault(logging.GetLogger())
	// Route the standard library logger to the same sink so Fyne/GLFW
	// diagnostics (which use the stdlib logger) are captured too.
	log.SetOutput(logOut)
	cfgLevel := config.GetConfig().Logging.Level
	slog.Debug("logging initialized", "level", cfgLevel)

	// Capture an otherwise-silent panic (e.g. an OpenGL/window init failure on
	// a GPU-less VM) to the log before the process exits.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("adder-tray panic",
				"panic", r,
				"stack", string(debug.Stack()))
			os.Exit(1)
		}
	}()

	// Refuse to run a second tray in the same session (Windows autostarts the
	// tray from the Run key and it can also be launched from the Start Menu).
	// A duplicate would fight the first over the engine lifecycle.
	if !acquireSingleInstance() {
		slog.Info("another adder-tray instance is already running; exiting")
		return
	}

	// Initialize Fyne app with a unique ID for professional persistence
	a := app.NewWithID("io.blinklabs.adder.tray")

	// Set application metadata
	a.SetIcon(assets.GetFullResource())

	application, err := tray.NewApp(a)
	if err != nil {
		slog.Error("failed to create application", "error", err)
		os.Exit(1)
	}

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("received shutdown signal")
		go func() {
			<-sigChan
			slog.Warn("received second signal, forcing exit")
			os.Exit(1)
		}()
		application.Shutdown()
	}()

	// Start the application. This blocks until a.Quit() is called.
	application.Run()
}

// openTrayLogFile opens (creating as needed) the tray's log file under the
// platform log directory, appending. It returns nil on failure so logging
// falls back to os.Stderr only — logging setup must never abort startup.
func openTrayLogFile() *os.File {
	dir := setup.LogDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("could not create log directory", "dir", dir, "error", err)
		return nil
	}
	path := filepath.Join(dir, "adder-tray.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		slog.Warn("could not open log file", "path", path, "error", err)
		return nil
	}
	return f
}
