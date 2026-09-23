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

package notify

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/gen2brain/beeep"
)

//go:embed icon.png
var icon []byte

type NotifyOutput struct {
	plugin.Base
	title string
}

func New(options ...NotifyOptionFunc) *NotifyOutput {
	n := &NotifyOutput{
		title: "Adder",
	}
	for _, option := range options {
		option(n)
	}
	return n
}

// Role identifies this plugin as a pipeline output.
func (n *NotifyOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start the notify output
func (n *NotifyOutput) Start() error {
	return n.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (n *NotifyOutput) StartContext(ctx context.Context) error {
	return n.StartRun(ctx,
		plugin.BaseConfig{HasInput: true, DrainOnStop: true},
		n.start,
		plugin.ShutdownHooks{},
	)
}

func (n *NotifyOutput) start(ctx context.Context) error {
	// Write our icon asset
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(fmt.Sprintf("%s/%s", userCacheDir, "adder")); os.IsNotExist(
		err,
	) {
		err = os.MkdirAll(
			fmt.Sprintf("%s/%s", userCacheDir, "adder"),
			os.ModePerm,
		)
		if err != nil {
			return fmt.Errorf("failed to create cache directory: %w", err)
		}
	}
	filename := fmt.Sprintf("%s/%s/%s", userCacheDir, "adder", "icon.png")
	if err := os.WriteFile(filename, icon, 0o600); err != nil {
		return fmt.Errorf("failed to write icon file: %w", err)
	}
	in := n.Input()
	n.Go(func() {
		for {
			evt, ok := <-in
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			switch evt.Type {
			case event.TypeBlock:
				payload := evt.Payload
				if payload == nil {
					slog.Error("block event has nil payload")
					continue
				}
				context := evt.Context
				if context == nil {
					slog.Error("block event has nil context")
					continue
				}

				be := payload.(event.BlockEvent)
				bc := context.(event.BlockContext)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf(
						"New Block!\nBlockNumber: %d, SlotNumber: %d, TransactionCount: %d\nHash: %s",
						bc.BlockNumber,
						bc.SlotNumber,
						be.TransactionCount,
						be.BlockHash,
					),
					filename,
				)
				if err != nil {
					slog.Error(
						"failed to send block notification",
						"error",
						err,
					)
					continue
				}
			case event.TypeRollback:
				payload := evt.Payload
				if payload == nil {
					slog.Error("rollback event has nil payload")
					continue
				}

				re := payload.(event.RollbackEvent)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf("Rollback!\nSlotNumber: %d\nBlockHash: %s",
						re.SlotNumber,
						re.BlockHash,
					),
					filename,
				)
				if err != nil {
					slog.Error(
						"failed to send rollback notification",
						"error",
						err,
					)
					continue
				}
			case event.TypeTransaction:
				payload := evt.Payload
				if payload == nil {
					slog.Error("transaction event has nil payload")
					continue
				}
				context := evt.Context
				if context == nil {
					slog.Error("transaction event has nil context")
					continue
				}

				te := payload.(event.TransactionEvent)
				tc := context.(event.TransactionContext)
				err := beeep.Notify(
					n.title,
					fmt.Sprintf(
						"New Transaction!\nBlockNumber: %d, SlotNumber: %d\nInputs: %d, Outputs: %d\nFee: %d\nHash: %s",
						tc.BlockNumber,
						tc.SlotNumber,
						len(te.Inputs),
						len(te.Outputs),
						te.Fee,
						tc.TransactionHash,
					),
					filename,
				)
				if err != nil {
					slog.Error(
						"failed to send transaction notification",
						"error",
						err,
					)
					continue
				}
			default:
				err := beeep.Notify(
					n.title,
					fmt.Sprintf("New Event!\nEvent: %v", evt),
					filename,
				)
				if err != nil {
					slog.Error(
						"failed to send notification",
						"error",
						err,
						"event_type",
						evt.Type,
					)
					continue
				}
			}
		}
	})
	return nil
}

// Stop the notify output
func (n *NotifyOutput) Stop() error {
	return n.Shutdown(plugin.ShutdownHooks{})
}

var _ plugin.ManagedPlugin = (*NotifyOutput)(nil)
