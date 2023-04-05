package chainsync

import (
	"github.com/blinklabs-io/gouroboros/ledger"
)

type TransactionEvent struct {
	BlockNumber     uint64           `json:"blockNumber"`
	BlockHash       string           `json:"blockHash"`
	SlotNumber      uint64           `json:"slotNumber"`
	TransactionHash string           `json:"transactionHash"`
	TransactionCbor byteSliceJsonHex `json:"transactionCbor"`
}

func NewTransactionEvent(block ledger.Block, txBody ledger.TransactionBody) TransactionEvent {
	evt := TransactionEvent{
		BlockNumber:     block.BlockNumber(),
		BlockHash:       block.Hash(),
		SlotNumber:      block.SlotNumber(),
		TransactionHash: txBody.Hash(),
		TransactionCbor: txBody.Cbor(),
	}
	return evt
}
