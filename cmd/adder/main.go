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
	"context"
	"errors"
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
		Use:   programName,
		Short: "Cardano blockchain event streaming tool",
		Long: `Adder tails the Cardano blockchain and emits structured events for blocks,
transactions, rollbacks, and governance actions. It uses a plugin-based
pipeline architecture with configurable inputs, filters, and outputs.

Input plugins:  chainsync (default), mempool, utxorpc
Output plugins: log (default), webhook, telegram, push, notify, notify-json
Use --output-log-level error for errors-only logging.
Filters:        address, asset, policy, pool, drep, event type

Events are also available via the /events WebSocket/SSE API endpoint.`,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd)
		},
	}
)

func slogPrintf(format string, v ...any) {
	logging.GetLoggerForComponent("maxprocs").Info(
		"maxprocs status",
		"message",
		fmt.Sprintf(format, v...),
	)
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
	rootCmd.AddCommand(newNotificationsCmd())
}

func run(cmd *cobra.Command) error {
	versionFlag, _ := cmd.Flags().GetBool("version")
	if versionFlag {
		fmt.Printf("%s %s\n", programName, version.GetVersionString())
		return nil
	}

	inputFlag, _ := cmd.Flags().GetString("input")
	if inputFlag == "list" {
		fmt.Printf("Available input plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeInput) {
			fmt.Printf("%- 14s %s\n", plugin.Name, plugin.Description)
		}
		return nil
	}

	outputFlag, _ := cmd.Flags().GetString("output")
	if outputFlag == "list" {
		fmt.Printf("Available output plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeOutput) {
			fmt.Printf("%- 14s %s\n", plugin.Name, plugin.Description)
		}
		return nil
	}

	configFile, _ := cmd.Flags().GetString("config")
	if err := cfg.LoadWithFlags(configFile, cmd.Flags()); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	resolved, err := cfg.ResolvePlugins(cmd.Flags())
	if err != nil {
		return fmt.Errorf("failed to resolve plugin config: %w", err)
	}
	if err := validateNotificationInput(resolved, cfg.Input, cfg.Output); err != nil {
		return err
	}

	logOptions, err := resolved.Options(plugin.PluginTypeOutput, "log")
	if err != nil {
		return err
	}
	level, err := logging.ParseLevel(logOptions.String("level"))
	if err != nil {
		return fmt.Errorf("output.log: %w", err)
	}
	logging.Configure(level)
	logger := logging.GetLogger()
	slog.SetDefault(logger)

	// Configure max processes with our logger wrapper, toss undo func
	_, err = maxprocs.Set(maxprocs.Logger(slogPrintf))
	if err != nil {
		// If we hit this, something really wrong happened
		logger.Error(err.Error())
		return err
	}

	// Create API instance with debug disabled
	apiInstance := api.New(false,
		api.WithGroup("/v1"),
		api.WithHost(cfg.Api.ListenAddress),
		api.WithPort(cfg.Api.ListenPort))

	// Create pipeline
	pipe := pipeline.New()

	// Register pipeline as health checker for API
	api.RegisterHealthChecker(pipe)

	// Configure input
	input, err := resolved.New(plugin.PluginTypeInput, cfg.Input)
	if err != nil {
		return err
	}
	pipe.AddInput(input)

	// Configure filters
	for _, filterEntry := range plugin.GetPlugins(plugin.PluginTypeFilter) {
		filter, err := resolved.New(plugin.PluginTypeFilter, filterEntry.Name)
		if err != nil {
			return err
		}
		pipe.AddFilter(filter)
	}

	if err := configureOutput(pipe, cfg.Output, resolved); err != nil {
		logger.Error("failed to configure output", "error", err)
		return err
	}

	// Start debug listener
	if cfg.Debug.ListenPort > 0 {
		logger.Info(
			"starting debug listener",
			"address",
			cfg.Debug.ListenAddress,
			"port",
			cfg.Debug.ListenPort,
		)
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
				logger.Error("failed to start debug listener", "error", err)
				os.Exit(1)
			}
		}()
	}

	// Create event hub for broadcasting pipeline events via /events endpoint
	eventHub := api.NewEventHub(cfg.Api.Events.BufferSize)
	defer eventHub.Close()
	observerCh := eventHub.InputChan()
	pipe.RegisterObserver(observerCh)
	apiInstance.HandleFunc("GET /events", eventHub.HandleEvents)

	// Start API after plugins are configured
	if err := apiInstance.Start(); err != nil {
		logger.Error("failed to start API", "error", err)
		return fmt.Errorf("failed to start API: %w", err)
	}
	defer func() {
		// Streaming handlers must exit before HTTP shutdown waits for them.
		eventHub.Close()
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer cancel()
		if err := apiInstance.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to stop API", "error", err)
		}
	}()

	// Start pipeline and wait for error
	if err := pipe.Start(); err != nil {
		logger.Error("failed to start pipeline", "error", err)
		return fmt.Errorf("failed to start pipeline: %w", err)
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Recoverable diagnostics do not decide lifecycle; Failed is independent
	// so terminal failure cannot be hidden behind a blocked error consumer.
	go func() {
		for err := range pipe.ErrorChan() {
			// Log error but keep running
			logger.Error("pipeline error", "error", err)
		}
		logger.Info("Error channel closed")
	}()

	logger.Info("Adder started, waiting for shutdown signal...")
	defer signal.Stop(sigChan)
	var terminalErr error
	select {
	case <-sigChan:
		logger.Info("Shutdown signal received, stopping pipeline...")
	case <-pipe.Failed():
		terminalErr = pipe.Failure()
		logger.Error(
			"terminal plugin failure, stopping pipeline",
			"error",
			terminalErr,
		)
	}

	// Graceful shutdown using Stop() method
	if err := pipe.Stop(); err != nil {
		logger.Error("failed to stop pipeline", "error", err)
		return errors.Join(
			terminalErr,
			fmt.Errorf("failed to stop pipeline: %w", err),
		)
	}
	eventHub.Close()
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()
	if err := apiInstance.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to stop API", "error", err)
		return errors.Join(
			terminalErr,
			fmt.Errorf("failed to stop API: %w", err),
		)
	}

	if terminalErr != nil {
		return terminalErr
	}
	logger.Info("Adder stopped gracefully")
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func configureOutput(
	pipe *pipeline.Pipeline,
	name string,
	resolved *plugin.Configuration,
) error {
	output, err := resolved.New(plugin.PluginTypeOutput, name)
	if err != nil {
		return err
	}
	if registrar, ok := output.(api.APIRouteRegistrar); ok {
		registrar.RegisterRoutes()
	}
	pipe.AddOutput(output)
	return nil
}
