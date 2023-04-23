package main

import (
	"fmt"
	"net/http"
	"os"

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
			fmt.Println(plugin)
		}
		return
	}

	if cfg.Output == "list" {
		fmt.Printf("Available output plugins:\n\n")
		for _, plugin := range plugin.GetPlugins(plugin.PluginTypeOutput) {
			fmt.Println(plugin)
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
		logger.Infof("starting debug listener on %s:%d", cfg.Debug.ListenAddress, cfg.Debug.ListenPort)
		go func() {
			err := http.ListenAndServe(fmt.Sprintf("%s:%d", cfg.Debug.ListenAddress, cfg.Debug.ListenPort), nil)
			if err != nil {
				logger.Fatalf("failed to start debug listener: %s", err)
			}
		}()
	}

	// Create pipeline
	pipe := pipeline.New()

	// Configure input
	input := plugin.GetPlugin(plugin.PluginTypeInput, cfg.Input)
	if input == nil {
		logger.Fatalf("unknown input: %s", cfg.Input)
	}
	pipe.AddInput(input)

	// Configure output
	output := plugin.GetPlugin(plugin.PluginTypeOutput, cfg.Output)
	if output == nil {
		logger.Fatalf("unknown output: %s", cfg.Output)
	}
	pipe.AddOutput(output)

	// Start pipeline and wait for error
	if err := pipe.Start(); err != nil {
		logger.Fatalf("failed to start pipeline: %s\n", err)
	}
	err, ok := <-pipe.ErrorChan()
	if ok {
		logger.Fatalf("pipeline failed: %s\n", err)
	}
}
