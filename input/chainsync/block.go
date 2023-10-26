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

type BlockContext struct {
	BlockNumber  uint64 `json:"blockNumber"`
	SlotNumber   uint64 `json:"slotNumber"`
	NetworkMagic uint32 `json:"networkMagic"`
}

type BlockEvent struct {
	BlockBodySize uint64           `json:"blockBodySize"`
	IssuerVkey    string           `json:"issuerVkey"`
	BlockHash     string           `json:"blockHash"`
	BlockCbor     byteSliceJsonHex `json:"blockCbor,omitempty"`
}

func NewBlockContext(block ledger.Block, networkMagic uint32) BlockContext {
	ctx := BlockContext{
		BlockNumber:  block.BlockNumber(),
		SlotNumber:   block.SlotNumber(),
		NetworkMagic: networkMagic,
	}
	return ctx
}

func NewBlockHeaderContext(block ledger.BlockHeader) BlockContext {
	ctx := BlockContext{
		BlockNumber: block.BlockNumber(),
		SlotNumber:  block.SlotNumber(),
	}
	return ctx
}

func NewBlockEvent(block ledger.Block, includeCbor bool) BlockEvent {
	evt := BlockEvent{
		BlockBodySize: block.BlockBodySize(),
		BlockHash:     block.Hash(),
		IssuerVkey:    block.IssuerVkey().Hash().String(),
	}
	if includeCbor {
		evt.BlockCbor = block.Cbor()
	}
	return evt
}
