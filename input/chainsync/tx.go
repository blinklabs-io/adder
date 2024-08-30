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

package chainsync

import (
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
)

type TransactionContext struct {
	BlockNumber     uint64 `json:"blockNumber"`
	SlotNumber      uint64 `json:"slotNumber"`
	TransactionHash string `json:"transactionHash"`
	TransactionIdx  uint32 `json:"transactionIdx"`
	NetworkMagic    uint32 `json:"networkMagic"`
}

type TransactionEvent struct {
	Transaction     ledger.Transaction         `json:"-"`
	BlockHash       string                     `json:"blockHash"`
	TransactionCbor byteSliceJsonHex           `json:"transactionCbor,omitempty"`
	Inputs          []ledger.TransactionInput  `json:"inputs"`
	Outputs         []ledger.TransactionOutput `json:"outputs"`
	Certificates    []ledger.Certificate       `json:"certificates,omitempty"`
	ReferenceInputs []ledger.TransactionInput  `json:"referenceInputs,omitempty"`
	Metadata        *cbor.LazyValue            `json:"metadata,omitempty"`
	Fee             uint64                     `json:"fee"`
	TTL             uint64                     `json:"ttl,omitempty"`
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
		TransactionHash: tx.Hash(),
		TransactionIdx:  index,
		NetworkMagic:    networkMagic,
	}
	return ctx
}

func NewTransactionEvent(
	block ledger.Block,
	tx ledger.Transaction,
	includeCbor bool,
) TransactionEvent {
	evt := TransactionEvent{
		Transaction: tx,
		BlockHash:   block.Hash(),
		Inputs:      tx.Inputs(),
		Outputs:     tx.Outputs(),
		Fee:         tx.Fee(),
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
	return evt
}
