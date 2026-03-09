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

package utxorpc

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/protocol/blockfetch"
	"github.com/blinklabs-io/gouroboros/protocol/common"
	cardanopb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
	syncpb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/sync"
	watchpb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/watch"
)

// mapFollowTipResponse maps a single FollowTipResponse into zero or more adder
// events, fanning out Apply actions into block + per-transaction events and
// emitting rollback events for Undo/Reset.
func mapFollowTipResponse(resp *syncpb.FollowTipResponse, includeCbor bool, networkMagic uint32) ([]event.Event, error) {
	if resp == nil {
		return nil, errors.New("response is nil")
	}

	if apply := resp.GetApply(); apply != nil {
		// CBOR path (preferred): decode NativeBytes via gouroboros.
		if nativeBytes := apply.GetNativeBytes(); nativeBytes != nil {
			return followTipApplyCBOR(nativeBytes, includeCbor, networkMagic)
		}
		// Protobuf fallback: extract fields from the parsed cardano.Block.
		if cb := apply.GetCardano(); cb != nil {
			return followTipApplyProtobuf(cb, networkMagic)
		}
		return nil, errors.New("utxorpc Apply: neither NativeBytes nor Cardano block present")
	}

	if undo := resp.GetUndo(); undo != nil {
		if b := undo.GetCardano(); b != nil && b.Header != nil {
			return []event.Event{
				event.New(
					"input.rollback",
					time.Now(),
					nil,
					event.NewRollbackEvent(common.NewPoint(b.Header.GetSlot(), b.Header.GetHash())),
				),
			}, nil
		}
	}

	if reset := resp.GetReset_(); reset != nil {
		return []event.Event{
			event.New(
				"input.rollback",
				time.Now(),
				nil,
				event.NewRollbackEvent(common.NewPoint(reset.GetSlot(), reset.GetHash())),
			),
		}, nil
	}

	return nil, nil
}

// followTipApplyCBOR decodes a block from NativeBytes (NtC wrapped CBOR) and
// fans out into block + per-transaction + governance + DRep events.
func followTipApplyCBOR(nativeBytes []byte, includeCbor bool, networkMagic uint32) ([]event.Event, error) {
	var wb blockfetch.WrappedBlock
	if _, err := cbor.Decode(nativeBytes, &wb); err != nil {
		return nil, fmt.Errorf("decode wrapped block: %w", err)
	}
	block, err := ledger.NewBlockFromCbor(wb.Type, wb.RawBlock)
	if err != nil {
		return nil, fmt.Errorf("decode block from CBOR: %w", err)
	}

	var out []event.Event
	out = append(out, event.New(
		"input.block",
		time.Now(),
		event.NewBlockHeaderContext(block.Header()),
		event.NewBlockEvent(block, includeCbor),
	))
	for t, transaction := range block.Transactions() {
		if t < 0 || t > math.MaxUint32 {
			return nil, errors.New("invalid number of transactions")
		}
		out = append(out, event.New(
			"input.transaction",
			time.Now(),
			event.NewTransactionContext(block, transaction, uint32(t), networkMagic),
			event.NewTransactionEvent(block, transaction, includeCbor, nil),
		))
		if event.HasGovernanceData(transaction) {
			out = append(out, event.New(
				"input.governance",
				time.Now(),
				event.NewGovernanceContext(block, transaction, uint32(t), networkMagic),
				event.NewGovernanceEvent(block, transaction, includeCbor),
			))
		}
		if drepCerts := event.ExtractDRepCertificates(transaction); len(drepCerts) > 0 {
			drepCtx := event.NewGovernanceContext(block, transaction, uint32(t), networkMagic)
			for _, cert := range drepCerts {
				if evtType, ok := event.DRepEventType(cert.CertificateType); ok {
					out = append(out, event.New(evtType, time.Now(), drepCtx,
						event.NewDRepCertificateEvent(block, cert)))
				}
			}
		}
	}
	return out, nil
}

// followTipApplyProtobuf constructs events from a parsed cardano.Block when
// NativeBytes is not available.
func followTipApplyProtobuf(cb *cardanopb.Block, networkMagic uint32) ([]event.Event, error) {
	header := cb.GetHeader()
	if header == nil {
		return nil, errors.New("utxorpc Apply protobuf: block header is nil")
	}

	now := time.Now()
	var out []event.Event
	out = append(out, event.New(
		"input.block",
		now,
		pbBlockContext(header, networkMagic),
		pbBlockEvent(cb),
	))

	blockHash := header.GetHash()
	body := cb.GetBody()
	if body == nil {
		return out, nil
	}
	for t, tx := range body.GetTx() {
		if t < 0 || t > math.MaxUint32 {
			return nil, errors.New("invalid number of transactions")
		}
		txHash := tx.GetHash()
		//nolint:gosec // t is bounds-checked above
		idx := uint32(t)
		out = append(out, event.New(
			"input.transaction",
			now,
			pbTransactionContext(header, txHash, idx, networkMagic),
			pbTransactionEvent(blockHash, tx),
		))
		if hasGovernanceData(tx) {
			out = append(out, event.New(
				"input.governance",
				now,
				pbGovernanceContext(header, txHash, idx, networkMagic),
				pbGovernanceEvent(blockHash, tx),
			))
		}
	}
	return out, nil
}

// mapWatchTxResponse maps a single WatchTxResponse into zero or more adder
// events. Apply events use the transaction from GetCardano() directly and
// extract block context from the accompanying block data. Undo emits a
// rollback event. Idle is handled at the call site.
func mapWatchTxResponse(resp *watchpb.WatchTxResponse, networkMagic uint32) ([]event.Event, error) {
	if resp == nil {
		return nil, nil
	}

	if apply := resp.GetApply(); apply != nil {
		cardanoTx := apply.GetCardano()
		if cardanoTx == nil || cardanoTx.GetHash() == nil {
			return nil, errors.New("utxorpc WatchTx Apply: cardano tx or hash is nil")
		}

		header := watchTxBlockHeader(apply.GetBlock())
		return watchTxApplyProtobuf(cardanoTx, header, networkMagic)
	}

	if undo := resp.GetUndo(); undo != nil {
		if blk := undo.GetBlock(); blk != nil {
			if b := blk.GetCardano(); b != nil && b.Header != nil {
				return []event.Event{
					event.New(
						"input.rollback",
						time.Now(),
						nil,
						event.NewRollbackEvent(common.NewPoint(
							b.Header.GetSlot(),
							b.Header.GetHash(),
						)),
					),
				}, nil
			}
		}
	}

	// Idle or unrecognised action — no events.
	return nil, nil
}

// watchTxBlockHeader extracts a block header from the block context attached
// to a WatchTx Apply response. It tries NativeBytes (CBOR) first, then the
// protobuf Cardano block. Returns nil if no header is available.
func watchTxBlockHeader(blk *watchpb.AnyChainBlock) *cardanopb.BlockHeader {
	if blk == nil {
		return nil
	}
	if nativeBytes := blk.GetNativeBytes(); nativeBytes != nil {
		var wb blockfetch.WrappedBlock
		if _, err := cbor.Decode(nativeBytes, &wb); err == nil {
			if block, err := ledger.NewBlockFromCbor(wb.Type, wb.RawBlock); err == nil {
				return &cardanopb.BlockHeader{
					Slot:   block.SlotNumber(),
					Hash:   block.Hash().Bytes(),
					Height: block.BlockNumber(),
				}
			}
		}
	}
	if cb := blk.GetCardano(); cb != nil {
		return cb.GetHeader()
	}
	return nil
}

// watchTxApplyProtobuf constructs transaction/governance events from a
// protobuf Tx and an optional block header for context. header may be nil
// if no block context is present.
func watchTxApplyProtobuf(
	tx *cardanopb.Tx,
	header *cardanopb.BlockHeader,
	networkMagic uint32,
) ([]event.Event, error) {
	if header == nil {
		header = &cardanopb.BlockHeader{}
	}
	txHash := tx.GetHash()
	blockHash := header.GetHash()
	now := time.Now()

	var out []event.Event
	out = append(out, event.New(
		"input.transaction",
		now,
		pbTransactionContext(header, txHash, 0, networkMagic),
		pbTransactionEvent(blockHash, tx),
	))

	if hasGovernanceData(tx) {
		out = append(out, event.New(
			"input.governance",
			now,
			pbGovernanceContext(header, txHash, 0, networkMagic),
			pbGovernanceEvent(blockHash, tx),
		))
	}

	return out, nil
}

// hasGovernanceData is a conservative heuristic for whether a UTxO RPC
// Cardano transaction contains governance-related data.
func hasGovernanceData(tx *cardanopb.Tx) bool {
	if tx == nil {
		return false
	}
	// Proposals correspond roughly to governance actions; if any exist we treat
	// this as a governance-bearing transaction.
	if len(tx.GetProposals()) > 0 {
		return true
	}
	// Additional heuristics could be added here if needed (e.g. voting data),
	// but proposals alone are a good initial signal.
	return false
}
