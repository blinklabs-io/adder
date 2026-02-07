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
	"encoding/json"
	"fmt"
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
	errorChan chan error
	eventChan chan event.Event
	doneChan  chan struct{}
	logger    plugin.Logger
	format    string
}

func New(options ...LogOptionFunc) *LogOutput {
	l := &LogOutput{
		format: FormatText,
	}
	for _, option := range options {
		option(l)
	}
	if l.logger == nil {
		l.logger = logging.GetLogger()
	}
	return l
}

// Start the log output
func (l *LogOutput) Start() error {
	l.eventChan = make(chan event.Event, 10)
	l.errorChan = make(chan error)
	l.doneChan = make(chan struct{})
	// Capture channels locally to avoid races with Stop()
	eventChan := l.eventChan
	doneChan := l.doneChan
	go func() {
		defer close(doneChan)
		for evt := range eventChan {
			switch l.format {
			case FormatJSON:
				l.writeJSON(evt)
			default:
				l.writeText(evt)
			}
		}
	}()
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

	fmt.Fprintln(os.Stdout, line)
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
	os.Stdout.Write(append(data, '\n'))
}

// Stop the log output
func (l *LogOutput) Stop() error {
	if l.eventChan != nil {
		close(l.eventChan)
		// Wait for the goroutine to finish processing
		if l.doneChan != nil {
			<-l.doneChan
		}
		l.eventChan = nil
	}
	if l.errorChan != nil {
		close(l.errorChan)
		l.errorChan = nil
	}
	return nil
}

// ErrorChan returns the plugin's error channel
func (l *LogOutput) ErrorChan() <-chan error {
	return l.errorChan
}

// InputChan returns the input event channel
func (l *LogOutput) InputChan() chan<- event.Event {
	return l.eventChan
}

// OutputChan always returns nil
func (l *LogOutput) OutputChan() <-chan event.Event {
	return nil
}
