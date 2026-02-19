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

package event

import (
	"encoding/json"
	"fmt"

	"github.com/blinklabs-io/gouroboros/ledger"
	lcommon "github.com/blinklabs-io/gouroboros/ledger/common"
)

type TransactionContext struct {
	TransactionHash string `json:"transactionHash"`
	BlockNumber     uint64 `json:"blockNumber"`
	SlotNumber      uint64 `json:"slotNumber"`
	TransactionIdx  uint32 `json:"transactionIdx"`
	NetworkMagic    uint32 `json:"networkMagic"`
}

type TransactionEvent struct {
	Transaction     ledger.Transaction            `json:"-"`
	Witnesses       lcommon.TransactionWitnessSet `json:"-"`
	Withdrawals     map[string]uint64             `json:"withdrawals,omitempty"`
	Metadata        lcommon.TransactionMetadatum  `json:"metadata,omitempty"`
	BlockHash       string                        `json:"blockHash"`
	ReferenceInputs []ledger.TransactionInput     `json:"referenceInputs,omitempty"`
	Certificates    []ledger.Certificate          `json:"certificates,omitempty"`
	Outputs         []ledger.TransactionOutput    `json:"outputs"`
	ResolvedInputs  []ledger.TransactionOutput    `json:"resolvedInputs,omitempty"`
	Inputs          []ledger.TransactionInput     `json:"inputs"`
	TransactionCbor byteSliceJsonHex              `json:"transactionCbor,omitempty"`
	Fee             uint64                        `json:"fee"`
	TTL             uint64                        `json:"ttl,omitempty"`
}

// MarshalJSON implements custom JSON marshaling for TransactionEvent.
// The Witnesses field cannot be marshaled directly because its concrete Conway
// representation uses map[RedeemerKey]RedeemerValue, which Go's JSON encoder
// does not support (map keys must be strings). This method converts redeemer
// keys to "tag:index" strings (e.g. "spend:0", "mint:1").
func (t TransactionEvent) MarshalJSON() ([]byte, error) {
	type witnessesJSON struct {
		Vkey            []lcommon.VkeyWitness            `json:"vkeyWitnesses,omitempty"`
		NativeScripts   []lcommon.NativeScript           `json:"nativeScripts,omitempty"`
		Bootstrap       []lcommon.BootstrapWitness       `json:"bootstrapWitnesses,omitempty"`
		PlutusData      []lcommon.Datum                  `json:"plutusData,omitempty"`
		PlutusV1Scripts []lcommon.PlutusV1Script         `json:"plutusV1Scripts,omitempty"`
		PlutusV2Scripts []lcommon.PlutusV2Script         `json:"plutusV2Scripts,omitempty"`
		PlutusV3Scripts []lcommon.PlutusV3Script         `json:"plutusV3Scripts,omitempty"`
		Redeemers       map[string]lcommon.RedeemerValue `json:"redeemers,omitempty"`
	}

	var witnesses *witnessesJSON
	if t.Witnesses != nil {
		witnesses = &witnessesJSON{
			Vkey:            t.Witnesses.Vkey(),
			NativeScripts:   t.Witnesses.NativeScripts(),
			Bootstrap:       t.Witnesses.Bootstrap(),
			PlutusData:      t.Witnesses.PlutusData(),
			PlutusV1Scripts: t.Witnesses.PlutusV1Scripts(),
			PlutusV2Scripts: t.Witnesses.PlutusV2Scripts(),
			PlutusV3Scripts: t.Witnesses.PlutusV3Scripts(),
		}
		for k, v := range t.Witnesses.Redeemers().Iter() {
			if witnesses.Redeemers == nil {
				witnesses.Redeemers = make(map[string]lcommon.RedeemerValue)
			}
			witnesses.Redeemers[fmt.Sprintf("%s:%d", redeemerTagString(k.Tag), k.Index)] = v
		}
		// Suppress empty witness objects from the JSON output.
		if len(witnesses.Vkey) == 0 && len(witnesses.NativeScripts) == 0 &&
			len(witnesses.Bootstrap) == 0 && len(witnesses.PlutusData) == 0 &&
			len(witnesses.PlutusV1Scripts) == 0 && len(witnesses.PlutusV2Scripts) == 0 &&
			len(witnesses.PlutusV3Scripts) == 0 && len(witnesses.Redeemers) == 0 {
			witnesses = nil
		}
	}

	// Alias breaks the MarshalJSON method set to prevent infinite recursion.
	type Alias TransactionEvent
	return json.Marshal(struct {
		*Alias
		Witnesses *witnessesJSON `json:"witnesses,omitempty"`
	}{
		Alias:     (*Alias)(&t),
		Witnesses: witnesses,
	})
}

func redeemerTagString(tag lcommon.RedeemerTag) string {
	switch tag {
	case lcommon.RedeemerTagSpend:
		return "spend"
	case lcommon.RedeemerTagMint:
		return "mint"
	case lcommon.RedeemerTagCert:
		return "cert"
	case lcommon.RedeemerTagReward:
		return "reward"
	case lcommon.RedeemerTagVoting:
		return "voting"
	case lcommon.RedeemerTagProposing:
		return "proposing"
	default:
		return fmt.Sprintf("%d", tag)
	}
}

func NewTransactionContext(
	block ledger.Block,
	tx ledger.Transaction,
	index uint32,
	networkMagic uint32,
) TransactionContext {
	ctx := TransactionContext{
		BlockNumber:     block.BlockNumber(),
		SlotNumber:      block.SlotNumber(),
		TransactionHash: tx.Hash().String(),
		TransactionIdx:  index,
		NetworkMagic:    networkMagic,
	}
	return ctx
}

// NewMempoolTransactionContext creates a context for a mempool (unconfirmed) transaction.
// SlotNumber is the mempool snapshot slot from the node; BlockNumber and TransactionIdx are zero.
func NewMempoolTransactionContext(
	tx ledger.Transaction,
	slotNumber uint64,
	networkMagic uint32,
) TransactionContext {
	return TransactionContext{
		TransactionHash: tx.Hash().String(),
		SlotNumber:      slotNumber,
		NetworkMagic:    networkMagic,
	}
}

// NewTransactionEventFromTx builds a TransactionEvent from a transaction only (no block).
// Used for mempool transactions; BlockHash is left empty.
func NewTransactionEventFromTx(tx ledger.Transaction, includeCbor bool) TransactionEvent {
	evt := TransactionEvent{
		Transaction: tx,
		Inputs:      tx.Inputs(),
		Outputs:     tx.Outputs(),
		Fee:         tx.Fee().Uint64(),
		Witnesses:   tx.Witnesses(),
	}
	if includeCbor {
		evt.TransactionCbor = tx.Cbor()
	}
	if tx.Certificates() != nil {
		evt.Certificates = tx.Certificates()
	}
	if tx.Metadata() != nil {
		evt.Metadata = tx.Metadata()
	}
	if tx.ReferenceInputs() != nil {
		evt.ReferenceInputs = tx.ReferenceInputs()
	}
	if tx.TTL() != 0 {
		evt.TTL = tx.TTL()
	}
	if withdrawals := tx.Withdrawals(); len(withdrawals) > 0 {
		evt.Withdrawals = make(map[string]uint64)
		for addr, amount := range withdrawals {
			evt.Withdrawals[addr.String()] = amount.Uint64()
		}
	}
	return evt
}

func NewTransactionEvent(
	block ledger.Block,
	tx ledger.Transaction,
	includeCbor bool,
	resolvedInputs []ledger.TransactionOutput,
) TransactionEvent {
	evt := TransactionEvent{
		Transaction: tx,
		BlockHash:   block.Hash().String(),
		Inputs:      tx.Inputs(),
		Outputs:     tx.Outputs(),
		Fee:         tx.Fee().Uint64(),
		Witnesses:   tx.Witnesses(),
	}
	if includeCbor {
		evt.TransactionCbor = tx.Cbor()
	}
	if tx.Certificates() != nil {
		evt.Certificates = tx.Certificates()
	}
	if tx.Metadata() != nil {
		evt.Metadata = tx.Metadata()
	}
	if tx.ReferenceInputs() != nil {
		evt.ReferenceInputs = tx.ReferenceInputs()
	}
	if tx.TTL() != 0 {
		evt.TTL = tx.TTL()
	}
	if len(resolvedInputs) > 0 {
		evt.ResolvedInputs = resolvedInputs
	}
	if withdrawals := tx.Withdrawals(); len(withdrawals) > 0 {
		evt.Withdrawals = make(map[string]uint64)
		for addr, amount := range withdrawals {
			evt.Withdrawals[addr.String()] = amount.Uint64()
		}
	}
	return evt
}
