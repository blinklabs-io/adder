package chainsync

import (
	"github.com/blinklabs-io/gouroboros/ledger"
)

type BlockEvent struct {
	BlockNumber uint64           `json:"blockNumber"`
	BlockHash   string           `json:"blockHash"`
	SlotNumber  uint64           `json:"slotNumber"`
	BlockCbor   byteSliceJsonHex `json:"blockCbor"`
}

func NewBlockEvent(block ledger.Block) BlockEvent {
	evt := BlockEvent{
		BlockNumber: block.BlockNumber(),
		BlockHash:   block.Hash(),
		SlotNumber:  block.SlotNumber(),
		BlockCbor:   block.Cbor(),
	}
	return evt
}
