package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/blinklabs-io/snek/input/chainsync"
	"github.com/blinklabs-io/snek/internal/config"
	"github.com/blinklabs-io/snek/internal/logging"
	"github.com/blinklabs-io/snek/output/log"
)

var cmdlineFlags struct {
	configFile string
}

func main() {
	flag.StringVar(&cmdlineFlags.configFile, "config", "", "path to config file to load")
	flag.Parse()

	// Load config
	cfg, err := config.Load(cmdlineFlags.configFile)
	if err != nil {
		fmt.Printf("Failed to load config: %s\n", err)
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

	// Configure input
	input := chainsync.New(
		chainsync.WithNetwork(cfg.Node.Network),
		chainsync.WithNetworkMagic(cfg.Node.NetworkMagic),
		chainsync.WithSocketPath(cfg.Node.SocketPath),
		chainsync.WithAddress(cfg.Node.Address),
		chainsync.WithPort(cfg.Node.Port),
		chainsync.WithNodeToNode(cfg.Node.UseNtN),
		// TODO: add intersect point(s)
		chainsync.WithIntersectTip(true),
	)
	if err := input.Start(); err != nil {
		logger.Fatalf("failed to start ChainSync input: %s", err)
	}

	// Configure output
	output := log.New()
	if err := output.Start(); err != nil {
		logger.Fatalf("failed to start Log output: %s", err)
	}

	// Process input events
	for {
		select {
		case evt, ok := <-input.EventChan():
			if !ok {
				return
			}
			output.EventChan() <- evt
		case err, ok := <-input.ErrorChan():
			if !ok {
				return
			}
			logger.Fatalf("output failure: %s", err)
		}
	}
}
