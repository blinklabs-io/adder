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

	"github.com/blinklabs-io/adder/event"
	filter_chainsync "github.com/blinklabs-io/adder/filter/cardano"
	input_chainsync "github.com/blinklabs-io/adder/input/chainsync"
	output_embedded "github.com/blinklabs-io/adder/output/embedded"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/kelseyhightower/envconfig"
)

// We parse environment variables using envconfig into this struct
// Note: SocketPath is parsed but not used in this example since we connect
// to a remote node using WithAddress instead. To use a local socket,
// uncomment WithSocketPath and comment out WithAddress below.
type Config struct {
	SocketPath string `split_words:"true"`
	Magic      uint32
}

func main() {
	cfg := Config{
		Magic:      764824073,
		SocketPath: "/ipc/node.socket",
	}
	// Parse environment variables
	if err := envconfig.Process("cardano_node", &cfg); err != nil {
		panic(err)
	}

	// Create pipeline
	p := pipeline.New()

	// Configure pipeline input
	inputOpts := []input_chainsync.ChainSyncOptionFunc{
		input_chainsync.WithAutoReconnect(true),
		input_chainsync.WithIntersectTip(true),
		input_chainsync.WithStatusUpdateFunc(updateStatus),
		input_chainsync.WithNetworkMagic(cfg.Magic),
		// input_chainsync.WithSocketPath(cfg.SocketPath),
		// Use this if you want to connect to a remote node and not SocketPath
		// IOG cardano node
		input_chainsync.WithAddress("52.15.49.197:3001"),
	}
	input := input_chainsync.New(
		inputOpts...,
	)
	p.AddInput(input)

	// Define poolids to filter on
	filterChainsync := filter_chainsync.New(
		// https://cexplorer.io/pool/pool16agnvfan65ypnswgg6rml52lqtcqe5guxltexkn82sqgj2crqtx
		// https://cexplorer.io/pool/pool12t3zmafwjqms7cuun86uwc8se4na07r3e5xswe86u37djr5f0lx
		filter_chainsync.WithPoolIds(
			[]string{
				"pool16agnvfan65ypnswgg6rml52lqtcqe5guxltexkn82sqgj2crqtx",
				"pool12t3zmafwjqms7cuun86uwc8se4na07r3e5xswe86u37djr5f0lx",
			},
		),
	)
	// Add poolids filter to pipeline
	p.AddFilter(filterChainsync)

	// Configure pipeline output
	output := output_embedded.New(
		output_embedded.WithCallbackFunc(handleEvent),
	)

	p.AddOutput(output)

	// Start pipeline
	if err := p.Start(); err != nil {
		slog.Error("failed to start pipeline", "error", err)
		return
	}

	// Start error handler
	for {
		err, ok := <-p.ErrorChan()
		if ok {
			slog.Error("pipeline failed", "error", err)
		} else {
			break
		}
	}
}

func handleEvent(evt event.Event) error {
	slog.Info("received event", "type", evt.Type)
	return nil
}

func updateStatus(status input_chainsync.ChainSyncStatus) {
	slog.Info("chainsync status update", "status", status)
}
