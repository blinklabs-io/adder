// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package chainsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	filtercardano "github.com/blinklabs-io/adder/filter/cardano"
	filterevent "github.com/blinklabs-io/adder/filter/event"
	outputlog "github.com/blinklabs-io/adder/output/log"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/gouroboros/ledger/conway"
	"github.com/blinklabs-io/gouroboros/protocol/blockfetch"
	protocolchainsync "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	protocolcommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/require"
)

type callbackLogInput struct{ *ChainSync }

func (c *callbackLogInput) Start() error { return c.StartContext(context.Background()) }

func (c *callbackLogInput) StartContext(ctx context.Context) error {
	// Supply callbacks deterministically without a node connection; keep the
	// real Base ports, pipeline forwarding, filters, and output worker.
	return c.StartRun(ctx, chainSyncBaseConfig(), func(context.Context) error { return nil }, c.shutdownHooks())
}

func TestChainSyncCallbacksReachLogOutput(t *testing.T) {
	for _, mode := range []string{"ntc", "ntn"} {
		for _, format := range []string{outputlog.FormatText, outputlog.FormatJSON} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				c := New()
				c.networkMagic = 764824073
				path := filepath.Join(t.TempDir(), "events.log")
				p := pipeline.New()
				p.AddInput(&callbackLogInput{c})
				p.AddFilter(filtercardano.New())
				p.AddFilter(filterevent.New())
				p.AddOutput(outputlog.New(outputlog.WithFormat(format), outputlog.WithFilePath(path)))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, p.StartContext(ctx))
				t.Cleanup(func() { require.NoError(t, p.Stop()) })

				tx := &conway.ConwayTransaction{Body: conway.ConwayTransactionBody{
					TxFee: 180000,
					TxProposalProcedures: []conway.ConwayProposalProcedure{{
						PPGovAction: conway.ConwayGovAction{Action: &common.InfoGovAction{Type: 6}},
					}},
				}}
				block := MockBlock{
					MockBlockHeader: MockBlockHeader{
						era: common.Era{Name: "Conway"}, blockNumber: 100, slotNumber: 2000,
						blockBodySize: 1024, issuerVkey: common.IssuerVkey{},
						hash: common.NewBlake2b256([]byte("block")),
					},
					transactions: []common.Transaction{tx},
				}
				if mode == "ntc" {
					require.NoError(t, c.handleRollForward(protocolchainsync.CallbackContext{}, 0, block, protocolchainsync.Tip{}))
				} else {
					require.NoError(t, c.handleBlockFetchBlock(blockfetch.CallbackContext{}, 0, block))
				}
				require.NoError(t, c.handleRollBackward(protocolchainsync.CallbackContext{}, protocolcommon.Point{Slot: 1999, Hash: []byte{0xaa, 0xbb}}, protocolchainsync.Tip{}))
				// Pipeline.Stop cancels forwarding rather than draining upstream;
				// wait for delivery before checking the output's drain/close path.
				require.Eventually(t, func() bool {
					data, err := os.ReadFile(path)
					return err == nil && strings.Count(string(data), "\n") == 4
				}, 3*time.Second, time.Millisecond)
				require.NoError(t, p.Stop())
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
				require.Len(t, lines, 4)
				if format == outputlog.FormatText {
					expected := []string{
						"BLOCK        slot=2000       block=100      hash=" + block.Hash().String() + " era=Conway  txs=1 size=1024",
						"TX           slot=2000       block=100      tx=" + tx.Hash().String() + " fee=180000 inputs=0 outputs=0",
						"GOVERNANCE   slot=2000       block=100      tx=" + tx.Hash().String() + " proposals=1 votes=0 certs=0",
						"ROLLBACK     slot=1999       hash=aabb",
					}
					for i, line := range lines {
						require.GreaterOrEqual(t, len(line), 20)
						_, err := time.Parse("2006-01-02 15:04:05", line[:19])
						require.NoError(t, err)
						require.Equal(t, " "+expected[i], line[19:])
					}
					return
				}
				types := []string{event.TypeBlock, event.TypeTransaction, event.TypeGovernance, event.TypeRollback}
				for i, line := range lines {
					var raw map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(line), &raw))
					require.JSONEq(t, `"`+types[i]+`"`, string(raw["type"]))
					var timestamp time.Time
					require.NoError(t, json.Unmarshal(raw["timestamp"], &timestamp))
					require.False(t, timestamp.IsZero())
					if i == 3 {
						require.Len(t, raw, 3)
						require.NotContains(t, raw, "context")
						require.JSONEq(t, `{"blockHash":"aabb","slotNumber":1999}`, string(raw["payload"]))
						continue
					}
					require.Len(t, raw, 4)
					if i == 0 {
						require.JSONEq(t, `{"era":"Conway","blockNumber":100,"slotNumber":2000,"networkMagic":764824073}`, string(raw["context"]))
						continue
					}
					require.JSONEq(t, `{"blockNumber":100,"slotNumber":2000,"networkMagic":764824073,"transactionIdx":0,"transactionHash":"`+tx.Hash().String()+`"}`, string(raw["context"]))
					var payload map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(raw["payload"], &payload))
					require.JSONEq(t, `"`+block.Hash().String()+`"`, string(payload["blockHash"]))
					require.NotContains(t, payload, "transactionCbor")
					if i == 1 {
						require.JSONEq(t, "180000", string(payload["fee"]))
						require.JSONEq(t, "[]", string(payload["inputs"]))
						require.JSONEq(t, "[]", string(payload["outputs"]))
					} else {
						var proposals []map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(payload["proposalProcedures"], &proposals))
						require.Len(t, proposals, 1)
						require.JSONEq(t, `"Info"`, string(proposals[0]["actionType"]))
						require.JSONEq(t, `{"info":{}}`, string(proposals[0]["actionData"]))
						require.JSONEq(t, `{"url":"","dataHash":""}`, string(proposals[0]["anchor"]))
					}
				}
			})
		}
	}
}
