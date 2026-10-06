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

package log

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const (
	FormatText = "text"
	FormatJSON = "json"
)

type LogOutput struct {
	plugin.Base
	format string
	path   string
	file   *os.File
	level  slog.Level
}

func New(options ...LogOptionFunc) *LogOutput {
	l := &LogOutput{
		format: FormatText,
	}
	for _, option := range options {
		option(l)
	}
	if l.Logger() == nil {
		l.SetLogger(logging.GetLogger())
	}
	return l
}

// Role identifies this plugin as a pipeline output.
func (l *LogOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start the log output
func (l *LogOutput) Start() error {
	return l.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (l *LogOutput) StartContext(ctx context.Context) error {
	return l.StartRun(ctx,
		plugin.BaseConfig{HasInput: true, DrainOnStop: true},
		l.start,
		l.shutdownHooks(),
	)
}

func (l *LogOutput) start(ctx context.Context) error {
	if l.path != "" {
		f, err := os.OpenFile(
			l.path,
			os.O_APPEND|os.O_CREATE|os.O_WRONLY,
			0o644,
		)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		l.file = f
	}

	in := l.Input()
	l.Go(func() {
		for evt := range in {
			if l.level > slog.LevelInfo {
				continue
			}
			switch l.format {
			case FormatJSON:
				l.writeJSON(evt)
			default:
				l.writeText(evt)
			}
		}
	})
	return nil
}

// writeText writes events in a human-readable format to stdout.
func (l *LogOutput) writeText(evt event.Event) {
	ts := evt.Timestamp.Format("2006-01-02 15:04:05")

	var line string
	switch payload := evt.Payload.(type) {
	case event.BlockEvent:
		ctx, _ := evt.Context.(event.BlockContext)
		line = fmt.Sprintf(
			"%s %-12s slot=%-10d block=%-8d hash=%s era=%-7s txs=%d size=%d",
			ts, "BLOCK",
			ctx.SlotNumber, ctx.BlockNumber,
			payload.BlockHash,
			ctx.Era,
			payload.TransactionCount,
			payload.BlockBodySize,
		)
	case event.TransactionEvent:
		ctx, _ := evt.Context.(event.TransactionContext)
		line = fmt.Sprintf(
			"%s %-12s slot=%-10d block=%-8d tx=%s fee=%d inputs=%d outputs=%d",
			ts, "TX",
			ctx.SlotNumber, ctx.BlockNumber,
			ctx.TransactionHash,
			payload.Fee,
			len(payload.Inputs), len(payload.Outputs),
		)
	case event.RollbackEvent:
		line = fmt.Sprintf(
			"%s %-12s slot=%-10d hash=%s",
			ts, "ROLLBACK",
			payload.SlotNumber,
			payload.BlockHash,
		)
	case event.GovernanceEvent:
		ctx, _ := evt.Context.(event.GovernanceContext)
		certs := len(payload.DRepCertificates) +
			len(payload.VoteDelegationCertificates) +
			len(payload.CommitteeCertificates)
		line = fmt.Sprintf(
			"%s %-12s slot=%-10d block=%-8d tx=%s proposals=%d votes=%d certs=%d",
			ts, "GOVERNANCE",
			ctx.SlotNumber, ctx.BlockNumber,
			ctx.TransactionHash,
			len(payload.ProposalProcedures),
			len(payload.VotingProcedures),
			certs,
		)
	default:
		line = fmt.Sprintf(
			"%s %-12s %+v",
			ts, evt.Type, evt.Payload,
		)
	}

	out := os.Stdout
	if l.file != nil {
		out = l.file
	}
	if _, err := fmt.Fprintln(out, line); err != nil {
		// Fallback to stderr if primary write fails
		fmt.Fprintf(
			os.Stderr,
			"failed to write log: %v; original: %s\n",
			err,
			line,
		)
	}
}

// writeJSON writes events as newline-delimited JSON to stdout.
// Errors are written to stderr to avoid corrupting the JSON stream.
func (l *LogOutput) writeJSON(evt event.Event) {
	data, err := json.Marshal(evt)
	if err != nil {
		fmt.Fprintf(
			os.Stderr,
			"error: failed to marshal event: %v\n",
			err,
		)
		return
	}
	out := os.Stdout
	if l.file != nil {
		out = l.file
	}
	if _, err := out.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "error writing JSON log: %v\n", err)
	}
}

// Stop the log output
func (l *LogOutput) Stop() error {
	// The file is closed in AfterWait so the drained events are written
	// before it goes away.
	return l.Shutdown(l.shutdownHooks())
}

func (l *LogOutput) shutdownHooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{
		AfterWait: func() error {
			if l.file == nil {
				return nil
			}
			if err := l.file.Close(); err != nil {
				if logger := l.Logger(); logger != nil {
					logger.Error("failed to close log file",
						"path", l.path,
						"error", err)
				}
			}
			l.file = nil
			return nil
		},
	}
}

var _ plugin.ManagedPlugin = (*LogOutput)(nil)
