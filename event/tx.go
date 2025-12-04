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
	Witnesses       lcommon.TransactionWitnessSet `json:"witnesses,omitempty"`
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
		Fee:         tx.Fee(),
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
			evt.Withdrawals[addr.String()] = amount
		}
	}
	return evt
}
