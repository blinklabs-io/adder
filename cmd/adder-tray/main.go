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
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/fyne/v2/app"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/ui/assets"
	"github.com/blinklabs-io/adder/tray"
)

func main() {
	// Initialize logging
	cfg := config.GetConfig()
	if err := cfg.Load(""); err != nil {
		slog.Warn("failed to load environment config", "error", err)
	}
	logging.Configure()
	slog.SetDefault(logging.GetLogger())
	cfgLevel := config.GetConfig().Logging.Level
	slog.Debug("logging initialized", "level", cfgLevel)

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
