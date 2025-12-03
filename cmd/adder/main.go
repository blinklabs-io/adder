// Copyright 2025 Blink Labs Software
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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/blinklabs-io/adder/api"
	_ "github.com/blinklabs-io/adder/filter"
	_ "github.com/blinklabs-io/adder/input"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/version"
	_ "github.com/blinklabs-io/adder/output"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/inconshreveable/mousetrap"
	"github.com/spf13/cobra"
	"go.uber.org/automaxprocs/maxprocs"
)

var (
	programName string = "adder"
	cfg                = config.GetConfig()
	rootCmd            = &cobra.Command{
		Use:          programName,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run()
		},
	}
)

func slogPrintf(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...))
}

func init() {
	if os.Args != nil && os.Args[0] != programName {
		programName = os.Args[0]
		rootCmd.Use = programName
	}

	// Bail if we were run via double click on Windows, borrowed from ngrok
	if runtime.GOOS == "windows" {
		if mousetrap.StartedByExplorer() {
			fmt.Println("Adder is a command line program.")
			fmt.Printf(
				"You need to open cmd.exe and run %s from the command line.\n",
				programName,
			)
			fmt.Printf(
				"Try %s --help to get program usage information.\n",
				programName,
			)
			time.Sleep(30 * time.Second)
			os.Exit(1)
		}
	}

	if err := cfg.BindFlags(rootCmd.Flags()); err != nil {
		panic(err)
	}
}

func run() error {
	if cfg.Version {
		fmt.Printf("%s %s\n", programName, version.GetVersionString())
		return nil
	}

	if cfg.Input == "list" {
		fmt.Printf("Available input plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeInput) {
			fmt.Printf("%- 14s %s\n", plugin.Name, plugin.Description)
		}
		return nil
	}

	if cfg.Output == "list" {
		fmt.Printf("Available output plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeOutput) {
			fmt.Printf("%- 14s %s\n", plugin.Name, plugin.Description)
		}
		return nil
	}

	// Load config
	if err := cfg.Load(cfg.ConfigFile); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Process config for plugins
	if err := plugin.ProcessConfig(cfg.Plugin); err != nil {
		return fmt.Errorf("failed to process plugin config: %w", err)
	}

	// Process env vars for plugins
	if err := plugin.ProcessEnvVars(); err != nil {
		return fmt.Errorf("failed to process env vars: %w", err)
	}

	// Configure logging
	logging.Configure()
	logger := logging.GetLogger()
	slog.SetDefault(logger)

	// Configure max processes with our logger wrapper, toss undo func
	_, err := maxprocs.Set(maxprocs.Logger(slogPrintf))
	if err != nil {
		// If we hit this, something really wrong happened
		logger.Error(err.Error())
		return err
	}

	// Start debug listener
	if cfg.Debug.ListenPort > 0 {
		logger.Info(fmt.Sprintf(
			"starting debug listener on %s:%d",
			cfg.Debug.ListenAddress,
			cfg.Debug.ListenPort,
		))
		go func() {
			debugger := &http.Server{
				Addr: fmt.Sprintf(
					"%s:%d",
					cfg.Debug.ListenAddress,
					cfg.Debug.ListenPort,
				),
				ReadHeaderTimeout: 60 * time.Second,
			}
			err := debugger.ListenAndServe()
			if err != nil {
				logger.Error(
					fmt.Sprintf("failed to start debug listener: %s", err),
				)
				os.Exit(1)
			}
		}()
	}

	// Create API instance with debug disabled
	apiInstance := api.New(false,
		api.WithGroup("/v1"),
		api.WithHost(cfg.Api.ListenAddress),
		api.WithPort(cfg.Api.ListenPort))

	// Create pipeline
	pipe := pipeline.New()

	// Configure input
	input := plugin.GetPlugin(plugin.PluginTypeInput, cfg.Input)
	if input == nil {
		logger.Error("unknown input: " + cfg.Input)
		return fmt.Errorf("unknown input: %s", cfg.Input)
	}
	pipe.AddInput(input)

	// Configure filters
	for _, filterEntry := range plugin.GetPlugins(plugin.PluginTypeFilter) {
		filter := plugin.GetPlugin(plugin.PluginTypeFilter, filterEntry.Name)
		pipe.AddFilter(filter)
	}

	// Configure output
	output := plugin.GetPlugin(plugin.PluginTypeOutput, cfg.Output)
	if output == nil {
		logger.Error("unknown output: " + cfg.Output)
		return fmt.Errorf("unknown output: %s", cfg.Output)
	}
	// Check if output plugin implements APIRouteRegistrar
	if registrar, ok := any(output).(api.APIRouteRegistrar); ok {
		registrar.RegisterRoutes()
	}
	pipe.AddOutput(output)

	// Start API after plugins are configured
	if err := apiInstance.Start(); err != nil {
		logger.Error(fmt.Sprintf("failed to start API: %s", err))
		return fmt.Errorf("failed to start API: %w", err)
	}

	// Start pipeline and wait for error
	if err := pipe.Start(); err != nil {
		logger.Error(fmt.Sprintf("failed to start pipeline: %s", err))
		return fmt.Errorf("failed to start pipeline: %w", err)
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle errors in background
	// DON'T exit on errors
	go func() {
		for err := range pipe.ErrorChan() {
			// Log error but keep running
			logger.Error(fmt.Sprintf("pipeline error: %s", err))
		}
		logger.Info("Error channel closed")
	}()

	logger.Info("Adder started, waiting for shutdown signal...")
	<-sigChan
	logger.Info("Shutdown signal received, stopping pipeline...")

	// Graceful shutdown using Stop() method
	if err := pipe.Stop(); err != nil {
		logger.Error(fmt.Sprintf("failed to stop pipeline: %s", err))
		return fmt.Errorf("failed to stop pipeline: %w", err)
	}

	logger.Info("Adder stopped gracefully")
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
