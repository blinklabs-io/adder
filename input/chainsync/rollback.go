package chainsync

import (
	"encoding/hex"

	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

type RollbackEvent struct {
	BlockHash  string `json:"blockHash"`
	SlotNumber uint64 `json:"slotNumber"`
}

func NewRollbackEvent(point ocommon.Point) RollbackEvent {
	blockHashHex := hex.EncodeToString(point.Hash)
	evt := RollbackEvent{
		BlockHash:  blockHashHex,
		SlotNumber: point.Slot,
	}
	return evt
}
