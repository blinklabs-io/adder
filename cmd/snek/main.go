// Copyright 2023 Blink Labs Software
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
	"net/http"
	"os"

	"github.com/blinklabs-io/snek/api"
	_ "github.com/blinklabs-io/snek/filter"
	_ "github.com/blinklabs-io/snek/input"
	"github.com/blinklabs-io/snek/internal/config"
	"github.com/blinklabs-io/snek/internal/logging"
	"github.com/blinklabs-io/snek/internal/version"
	_ "github.com/blinklabs-io/snek/output"
	"github.com/blinklabs-io/snek/pipeline"
	"github.com/blinklabs-io/snek/plugin"
)

const (
	programName = "snek"
)

func main() {
	cfg := config.GetConfig()

	if err := cfg.ParseCmdlineArgs(programName, os.Args[1:]); err != nil {
		fmt.Printf("Failed to parse commandline: %s\n", err)
		os.Exit(1)
	}

	if cfg.Version {
		fmt.Printf("%s %s\n", programName, version.GetVersionString())
		os.Exit(0)
	}

	if cfg.Input == "list" {
		fmt.Printf("Available input plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeInput) {
			fmt.Printf("%- 14s %s\n", plugin.Name, plugin.Description)
		}
		return
	}

	if cfg.Output == "list" {
		fmt.Printf("Available output plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeOutput) {
			fmt.Printf("%- 14s %s\n", plugin.Name, plugin.Description)
		}
		return
	}

	// Load config
	if err := cfg.Load(cfg.ConfigFile); err != nil {
		fmt.Printf("Failed to load config: %s\n", err)
		os.Exit(1)
	}

	// Process config for plugins
	if err := plugin.ProcessConfig(cfg.Plugin); err != nil {
		fmt.Printf("Failed to process plugin config: %s\n", err)
		os.Exit(1)
	}

	// Process env vars for plugins
	if err := plugin.ProcessEnvVars(); err != nil {
		fmt.Printf("Failed to process env vars: %s\n", err)
		os.Exit(1)
	}

	// Configure logging
	logging.Configure()
	logger := logging.GetLogger()
	// Sync logger on exit
	defer func() {
		if err := logger.Sync(); err != nil {
			// We don't actually care about the error here, but we have to do something
			// to appease the linter
			return
		}
	}()

	// Start debug listener
	if cfg.Debug.ListenPort > 0 {
		logger.Infof(
			"starting debug listener on %s:%d",
			cfg.Debug.ListenAddress,
			cfg.Debug.ListenPort,
		)
		go func() {
			err := http.ListenAndServe(
				fmt.Sprintf(
					"%s:%d",
					cfg.Debug.ListenAddress,
					cfg.Debug.ListenPort,
				),
				nil,
			)
			if err != nil {
				logger.Fatalf("failed to start debug listener: %s", err)
			}
		}()
	}

	// Create API instance with debug disabled
	apiInstance := api.New(false,
		api.WithGroup("/v1"),
		api.WithPort("8080"))

	// Create pipeline
	pipe := pipeline.New()

	// Configure input
	input := plugin.GetPlugin(plugin.PluginTypeInput, cfg.Input)
	if input == nil {
		logger.Fatalf("unknown input: %s", cfg.Input)
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
		logger.Fatalf("unknown output: %s", cfg.Output)
	}
	// Check if output plugin implements APIRouteRegistrar
	if registrar, ok := interface{}(output).(api.APIRouteRegistrar); ok {
		registrar.RegisterRoutes()
	}
	pipe.AddOutput(output)

	// Start API after plugins are configured
	if err := apiInstance.Start(); err != nil {
		logger.Fatalf("failed to start API: %s", err)
	}

	// Start pipeline and wait for error
	if err := pipe.Start(); err != nil {
		logger.Fatalf("failed to start pipeline: %s", err)
	}
	err, ok := <-pipe.ErrorChan()
	if ok {
		logger.Fatalf("pipeline failed: %s", err)
	}
}
