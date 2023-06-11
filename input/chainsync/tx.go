// Copyright 2023 Blink Labs, LLC.
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
	"github.com/blinklabs-io/gouroboros/ledger"
)

type TransactionEvent struct {
	BlockNumber     uint64                     `json:"blockNumber"`
	BlockHash       string                     `json:"blockHash"`
	SlotNumber      uint64                     `json:"slotNumber"`
	TransactionHash string                     `json:"transactionHash"`
	TransactionCbor byteSliceJsonHex           `json:"transactionCbor,omitempty"`
	Inputs          []ledger.TransactionInput  `json:"inputs"`
	Outputs         []ledger.TransactionOutput `json:"outputs"`
}

func NewTransactionEvent(block ledger.Block, txBody ledger.TransactionBody, includeCbor bool) TransactionEvent {
	evt := TransactionEvent{
		BlockNumber:     block.BlockNumber(),
		BlockHash:       block.Hash(),
		SlotNumber:      block.SlotNumber(),
		TransactionHash: txBody.Hash(),
		Inputs:          txBody.Inputs(),
		Outputs:         txBody.Outputs(),
	}
	if includeCbor {
		evt.TransactionCbor = txBody.Cbor()
	}
	return evt
}
