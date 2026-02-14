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

package log

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaults(t *testing.T) {
	l := New()
	assert.Equal(t, FormatText, l.format)
}

func TestNewWithOptions(t *testing.T) {
	l := New(
		WithFormat(FormatJSON),
	)
	assert.Equal(t, FormatJSON, l.format)
}

func TestFormatJSONOutput(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	l := New(WithFormat(FormatJSON))
	require.NoError(t, l.Start())

	testEvent := event.New(
		"input.block",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		map[string]any{"blockNumber": 12345},
		map[string]any{"hash": "abc123"},
	)

	l.InputChan() <- testEvent
	require.NoError(t, l.Stop())

	// Read captured output
	w.Close()
	output, err := io.ReadAll(r)
	require.NoError(t, err)

	// Verify it's valid JSON
	line := strings.TrimSpace(string(output))
	assert.NotEmpty(t, line)

	var parsed event.Event
	err = json.Unmarshal([]byte(line), &parsed)
	require.NoError(t, err, "output should be valid JSON: %s", line)

	assert.Equal(t, "input.block", parsed.Type)
	assert.Equal(
		t,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		parsed.Timestamp,
	)
}

func TestFormatJSONNoSlogWrapper(t *testing.T) {
	// Verify JSON output does NOT contain slog fields like "level" or "msg"
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	l := New(WithFormat(FormatJSON))
	require.NoError(t, l.Start())

	testEvent := event.New(
		"input.transaction",
		time.Now(),
		nil,
		map[string]any{"fee": 200000},
	)

	l.InputChan() <- testEvent
	require.NoError(t, l.Stop())

	w.Close()
	output, err := io.ReadAll(r)
	require.NoError(t, err)

	line := strings.TrimSpace(string(output))

	// Should NOT have slog envelope fields
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &raw))
	assert.NotContains(t, raw, "level", "JSON output should not have slog 'level' field")
	assert.NotContains(t, raw, "msg", "JSON output should not have slog 'msg' field")
	assert.NotContains(t, raw, "component", "JSON output should not have slog 'component' field")

	// Should have event fields
	assert.Contains(t, raw, "type")
	assert.Contains(t, raw, "timestamp")
	assert.Contains(t, raw, "payload")
	assert.Equal(t, "input.transaction", raw["type"])
}

func TestFormatTextBlock(t *testing.T) {
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	l := New(WithFormat(FormatText))
	require.NoError(t, l.Start())

	ts := time.Date(2026, 1, 15, 14, 30, 45, 0, time.UTC)
	testEvent := event.New(
		"input.block",
		ts,
		event.BlockContext{
			Era:         "Conway",
			BlockNumber: 9876543,
			SlotNumber:  12345678,
		},
		event.BlockEvent{
			BlockHash:        "abc12345def67890",
			TransactionCount: 5,
			BlockBodySize:    1234,
		},
	)

	l.InputChan() <- testEvent
	require.NoError(t, l.Stop())

	w.Close()
	output, err := io.ReadAll(r)
	require.NoError(t, err)

	line := strings.TrimSpace(string(output))
	assert.Contains(t, line, "2026-01-15 14:30:45")
	assert.Contains(t, line, "BLOCK")
	assert.Contains(t, line, "slot=12345678")
	assert.Contains(t, line, "block=9876543")
	assert.Contains(t, line, "abc12345def67890")
	assert.Contains(t, line, "era=Conway")
	assert.Contains(t, line, "txs=5")
	assert.Contains(t, line, "size=1234")
}

func TestFormatTextTransaction(t *testing.T) {
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	l := New(WithFormat(FormatText))
	require.NoError(t, l.Start())

	ts := time.Date(2026, 1, 15, 14, 30, 45, 0, time.UTC)
	testEvent := event.New(
		"input.transaction",
		ts,
		event.TransactionContext{
			TransactionHash: "deadbeef12345678",
			BlockNumber:     100,
			SlotNumber:      200,
		},
		event.TransactionEvent{
			Fee: 180000,
		},
	)

	l.InputChan() <- testEvent
	require.NoError(t, l.Stop())

	w.Close()
	output, err := io.ReadAll(r)
	require.NoError(t, err)

	line := strings.TrimSpace(string(output))
	assert.Contains(t, line, "TX")
	assert.Contains(t, line, "tx=deadbeef12345678")
	assert.Contains(t, line, "fee=180000")
	assert.Contains(t, line, "inputs=0")
	assert.Contains(t, line, "outputs=0")
}

func TestFormatTextRollback(t *testing.T) {
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	l := New(WithFormat(FormatText))
	require.NoError(t, l.Start())

	ts := time.Date(2026, 1, 15, 14, 30, 45, 0, time.UTC)
	testEvent := event.New(
		"input.rollback",
		ts,
		nil,
		event.RollbackEvent{
			BlockHash:  "aabbccdd11223344",
			SlotNumber: 999,
		},
	)

	l.InputChan() <- testEvent
	require.NoError(t, l.Stop())

	w.Close()
	output, err := io.ReadAll(r)
	require.NoError(t, err)

	line := strings.TrimSpace(string(output))
	assert.Contains(t, line, "ROLLBACK")
	assert.Contains(t, line, "slot=999")
	assert.Contains(t, line, "aabbccdd11223344")
}

func TestFormatTextGovernance(t *testing.T) {
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	l := New(WithFormat(FormatText))
	require.NoError(t, l.Start())

	ts := time.Date(2026, 1, 15, 14, 30, 45, 0, time.UTC)
	testEvent := event.New(
		"input.governance",
		ts,
		event.GovernanceContext{
			TransactionHash: "govtx12345678abc",
			BlockNumber:     500,
			SlotNumber:      600,
		},
		event.GovernanceEvent{
			ProposalProcedures: []event.ProposalProcedureData{
				{ActionType: "Info"},
			},
			VotingProcedures: []event.VotingProcedureData{
				{Vote: "Yes"},
				{Vote: "No"},
			},
			DRepCertificates: []event.DRepCertificateData{
				{CertificateType: "Registration"},
			},
		},
	)

	l.InputChan() <- testEvent
	require.NoError(t, l.Stop())

	w.Close()
	output, err := io.ReadAll(r)
	require.NoError(t, err)

	line := strings.TrimSpace(string(output))
	assert.Contains(t, line, "GOVERNANCE")
	assert.Contains(t, line, "tx=govtx12345678abc")
	assert.Contains(t, line, "proposals=1")
	assert.Contains(t, line, "votes=2")
	assert.Contains(t, line, "certs=1")
}

func TestFormatConstants(t *testing.T) {
	assert.Equal(t, "text", FormatText)
	assert.Equal(t, "json", FormatJSON)
}

func TestStopIdempotent(t *testing.T) {
	l := New()
	require.NoError(t, l.Start())
	require.NoError(t, l.Stop())
	// Second stop should not panic
	require.NoError(t, l.Stop())
}

func TestOutputChanReturnsNil(t *testing.T) {
	l := New()
	assert.Nil(t, l.OutputChan())
}
