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
	"log/slog"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyOutputNew(t *testing.T) {
	t.Run("default title", func(t *testing.T) {
		n := New()
		assert.Equal(t, "Adder", n.title)
	})

	t.Run("custom title", func(t *testing.T) {
		n := New(WithTitle("Custom Title"))
		assert.Equal(t, "Custom Title", n.title)
	})
}

// captureHandler is a slog.Handler that forwards every emitted record to a
// channel so tests can deterministically wait for a log line without sleeping.
type captureHandler struct {
	records chan slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	// Non-blocking send so a slow/absent reader can never wedge the plugin
	// goroutine under -race.
	select {
	case h.records <- r:
	default:
	}
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(_ string) slog.Handler { return h }

// TestNotifyOutputMalformedPayload feeds events with nil payload/context
// through the running plugin and asserts each is logged and skipped without
// panicking. The malformed branches were converted from panic() to
// slog.Error+continue (commit 681ca5a); this guards against a regression that
// re-introduces the panic. A panic in the Start() goroutine would crash the
// test binary, so a clean completion is itself the "no panic" assertion.
func TestNotifyOutputMalformedPayload(t *testing.T) {
	// The malformed branches log via the global slog default, not the
	// plugin's injected logger, so we capture the default handler and
	// restore it afterwards. No t.Parallel(): we mutate global state.
	records := make(chan slog.Record, 16)
	prev := slog.Default()
	slog.SetDefault(slog.New(&captureHandler{records: records}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tests := []struct {
		name    string
		evt     event.Event
		wantMsg string
	}{
		{
			name:    "block event nil payload",
			evt:     event.Event{Type: "input.block", Payload: nil},
			wantMsg: "block event has nil payload",
		},
		{
			name: "block event nil context",
			evt: event.Event{
				Type:    "input.block",
				Payload: event.BlockEvent{},
				Context: nil,
			},
			wantMsg: "block event has nil context",
		},
		{
			name:    "rollback event nil payload",
			evt:     event.Event{Type: "input.rollback", Payload: nil},
			wantMsg: "rollback event has nil payload",
		},
		{
			name:    "transaction event nil payload",
			evt:     event.Event{Type: "input.transaction", Payload: nil},
			wantMsg: "transaction event has nil payload",
		},
		{
			name: "transaction event nil context",
			evt: event.Event{
				Type:    "input.transaction",
				Payload: event.TransactionEvent{},
				Context: nil,
			},
			wantMsg: "transaction event has nil context",
		},
	}

	n := New()
	require.NoError(t, n.Start())
	defer func() {
		require.NoError(t, n.Stop())
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Drain any stray records from a previous case so we match the
			// record produced by this event.
			for {
				select {
				case <-records:
					continue
				default:
				}
				break
			}

			n.InputChan() <- tt.evt

			select {
			case r := <-records:
				assert.Equal(t, tt.wantMsg, r.Message)
				assert.Equal(t, slog.LevelError, r.Level)
			case <-time.After(2 * time.Second):
				t.Fatalf(
					"timed out waiting for error log for %q",
					tt.name,
				)
			}
		})
	}
}
