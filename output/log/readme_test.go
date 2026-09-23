// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/stretchr/testify/require"
)

func TestREADMETextExamples(t *testing.T) {
	events := []event.Event{
		{Type: event.TypeBlock,
			Context: event.BlockContext{SlotNumber: 2000, BlockNumber: 100, Era: "Babbage"},
			Payload: event.BlockEvent{BlockHash: "abc123hash", TransactionCount: 5, BlockBodySize: 1024}},
		{Type: event.TypeTransaction,
			Context: event.TransactionContext{SlotNumber: 2000, BlockNumber: 100, TransactionHash: "deadbeef12345678"},
			Payload: event.TransactionEvent{Fee: 180000, Inputs: make([]ledger.TransactionInput, 2), Outputs: make([]ledger.TransactionOutput, 3)}},
		{Type: event.TypeRollback,
			Payload: event.RollbackEvent{SlotNumber: 2000, BlockHash: "aabbccdd11223344"}},
		{Type: event.TypeGovernance,
			Context: event.GovernanceContext{SlotNumber: 2000, BlockNumber: 100, TransactionHash: "govtx12345678abc"},
			Payload: event.GovernanceEvent{
				ProposalProcedures: []event.ProposalProcedureData{{ActionType: "Info"}},
				VotingProcedures:   []event.VotingProcedureData{{Vote: "Yes"}, {Vote: "No"}},
				DRepCertificates:   []event.DRepCertificateData{{CertificateType: "Registration"}},
			}},
	}
	const expected = "2026-05-24 12:00:00 BLOCK        slot=2000       block=100      hash=abc123hash era=Babbage txs=5 size=1024\n" +
		"2026-05-24 12:00:01 TX           slot=2000       block=100      tx=deadbeef12345678 fee=180000 inputs=2 outputs=3\n" +
		"2026-05-24 12:00:02 ROLLBACK     slot=2000       hash=aabbccdd11223344\n" +
		"2026-05-24 12:00:03 GOVERNANCE   slot=2000       block=100      tx=govtx12345678abc proposals=1 votes=2 certs=1\n"
	path := filepath.Join(t.TempDir(), "events.log")
	l := New(WithFilePath(path))
	require.NoError(t, l.Start())
	t.Cleanup(func() { require.NoError(t, l.Stop()) })
	for i, evt := range events {
		evt.Timestamp = time.Date(2026, 5, 24, 12, 0, i, 0, time.UTC)
		select {
		case l.InputChan() <- evt:
		case <-time.After(time.Second):
			t.Fatal("log output stopped consuming events")
		}
	}
	require.NoError(t, l.Stop())
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, expected, string(actual))
	readme, err := os.ReadFile("../../README.md")
	require.NoError(t, err)
	require.Contains(t, string(readme), "  ```text\n  "+strings.ReplaceAll(strings.TrimSuffix(expected, "\n"), "\n", "\n  ")+"\n  ```")
}
