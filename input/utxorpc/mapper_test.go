package utxorpc

import (
	_ "embed"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/protocol/blockfetch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cardanopb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
	syncpb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/sync"
	watchpb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/watch"
)

//go:embed testdata/shelley_block.hex
var shelleyBlockHex string

const blockTypeShelley = 2

func wrappedShelleyBlock() []byte {
	blockCbor, err := hex.DecodeString(strings.TrimSpace(shelleyBlockHex))
	if err != nil {
		panic(err)
	}
	wb := blockfetch.WrappedBlock{Type: blockTypeShelley, RawBlock: cbor.RawMessage(blockCbor)}
	encoded, err := cbor.Encode(wb)
	if err != nil {
		panic(err)
	}
	return encoded
}

func decodeShelleyBlock(t *testing.T) (ledger.Block, []byte) {
	t.Helper()
	nativeBytes := wrappedShelleyBlock()
	var wb blockfetch.WrappedBlock
	_, err := cbor.Decode(nativeBytes, &wb)
	require.NoError(t, err)
	block, err := ledger.NewBlockFromCbor(wb.Type, wb.RawBlock)
	require.NoError(t, err)
	require.Greater(t, len(block.Transactions()), 0)
	return block, nativeBytes
}

// --------------- FollowTip: CBOR path ---------------

func TestFollowTipApplyCBORFansOut(t *testing.T) {
	nativeBytes := wrappedShelleyBlock()

	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Apply{
			Apply: &syncpb.AnyChainBlock{
				NativeBytes: nativeBytes,
			},
		},
	}

	evts, err := mapFollowTipResponse(resp, false, 764824073)
	require.NoError(t, err)
	require.Greater(t, len(evts), 1, "should produce at least block + transaction events")
	assert.Equal(t, "input.block", evts[0].Type)
	for _, e := range evts[1:] {
		assert.Contains(t,
			[]string{"input.transaction", "input.governance", "input.drep-registration", "input.drep-update", "input.drep-retirement"},
			e.Type,
		)
	}
}

// --------------- FollowTip: Protobuf fallback ---------------

func TestFollowTipApplyProtobufFansOut(t *testing.T) {
	txHash, _ := hex.DecodeString("aabbccdd")
	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Apply{
			Apply: &syncpb.AnyChainBlock{
				Chain: &syncpb.AnyChainBlock_Cardano{
					Cardano: &cardanopb.Block{
						Header: &cardanopb.BlockHeader{
							Slot:   100,
							Hash:   []byte{0x01, 0x02},
							Height: 50,
						},
						Body: &cardanopb.BlockBody{
							Tx: []*cardanopb.Tx{
								{
									Hash:   txHash,
									Fee:    &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 200000}},
									Inputs: []*cardanopb.TxInput{{TxHash: []byte{0xaa}, OutputIndex: 0}},
									Outputs: []*cardanopb.TxOutput{{
										Address: []byte{0x61, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
										Coin:    &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 5000000}},
									}},
								},
							},
						},
					},
				},
			},
		},
	}

	evts, err := mapFollowTipResponse(resp, false, 764824073)
	require.NoError(t, err)
	require.Len(t, evts, 2, "block + 1 transaction")
	assert.Equal(t, "input.block", evts[0].Type)
	assert.Equal(t, "input.transaction", evts[1].Type)

	blockEvt := evts[0].Payload.(event.BlockEvent)
	assert.Equal(t, uint64(1), blockEvt.TransactionCount)

	txCtx := evts[1].Context.(event.TransactionContext)
	assert.Equal(t, hex.EncodeToString(txHash), txCtx.TransactionHash)
	assert.Equal(t, uint64(50), txCtx.BlockNumber)
	assert.Equal(t, uint64(100), txCtx.SlotNumber)
	assert.Equal(t, uint32(764824073), txCtx.NetworkMagic)

	txEvt := evts[1].Payload.(event.TransactionEvent)
	assert.Equal(t, uint64(200000), txEvt.Fee)
	assert.Len(t, txEvt.Inputs, 1)
	assert.Len(t, txEvt.Outputs, 1)
}

func TestFollowTipApplyProtobufGovernance(t *testing.T) {
	rewardAcct := []byte{0x61, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Apply{
			Apply: &syncpb.AnyChainBlock{
				Chain: &syncpb.AnyChainBlock_Cardano{
					Cardano: &cardanopb.Block{
						Header: &cardanopb.BlockHeader{Slot: 10, Hash: []byte{0x01}, Height: 5},
						Body: &cardanopb.BlockBody{
							Tx: []*cardanopb.Tx{
								{
									Hash: []byte{0xcc},
									Proposals: []*cardanopb.GovernanceActionProposal{
										{
											Deposit:       &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 500000000}},
											RewardAccount: rewardAcct,
											GovAction: &cardanopb.GovernanceAction{
												GovernanceAction: &cardanopb.GovernanceAction_InfoAction{InfoAction: 6},
											},
											Anchor: &cardanopb.Anchor{
												Url:         "https://example.com/proposal.json",
												ContentHash: []byte{0xab, 0xcd},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	evts, err := mapFollowTipResponse(resp, false, 0)
	require.NoError(t, err)
	require.Len(t, evts, 3, "block + tx + governance")
	assert.Equal(t, "input.block", evts[0].Type)
	assert.Equal(t, "input.transaction", evts[1].Type)
	assert.Equal(t, "input.governance", evts[2].Type)

	govEvt := evts[2].Payload.(event.GovernanceEvent)
	require.Len(t, govEvt.ProposalProcedures, 1)
	prop := govEvt.ProposalProcedures[0]
	assert.Equal(t, "Info", prop.ActionType)
	assert.Equal(t, uint64(500000000), prop.Deposit)
	assert.NotEmpty(t, prop.RewardAccount)
	assert.Equal(t, "https://example.com/proposal.json", prop.Anchor.Url)
	assert.Equal(t, "abcd", prop.Anchor.DataHash)
	assert.NotNil(t, prop.ActionData.Info)
}

func TestFollowTipApplyNeitherPathErrors(t *testing.T) {
	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Apply{
			Apply: &syncpb.AnyChainBlock{},
		},
	}
	_, err := mapFollowTipResponse(resp, false, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither NativeBytes nor Cardano block")
}

// --------------- FollowTip: Undo / Reset ---------------

func TestFollowTipUndoProducesRollback(t *testing.T) {
	hashBytes, _ := hex.DecodeString("02")
	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Undo{
			Undo: &syncpb.AnyChainBlock{
				Chain: &syncpb.AnyChainBlock_Cardano{
					Cardano: &cardanopb.Block{
						Header: &cardanopb.BlockHeader{Slot: 20, Hash: hashBytes, Height: 7},
					},
				},
			},
		},
	}
	evts, err := mapFollowTipResponse(resp, false, 0)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.rollback", evts[0].Type)
}

func TestFollowTipUndoNativeBytesProducesRollback(t *testing.T) {
	block, nativeBytes := decodeShelleyBlock(t)

	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Undo{
			Undo: &syncpb.AnyChainBlock{
				NativeBytes: nativeBytes,
			},
		},
	}
	evts, err := mapFollowTipResponse(resp, false, 0)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.rollback", evts[0].Type)

	rb := evts[0].Payload.(event.RollbackEvent)
	assert.Equal(t, block.SlotNumber(), rb.SlotNumber)
}

func TestFollowTipResetProducesRollback(t *testing.T) {
	hashBytes, _ := hex.DecodeString("03")
	resp := &syncpb.FollowTipResponse{
		Action: &syncpb.FollowTipResponse_Reset_{
			Reset_: &syncpb.BlockRef{Slot: 30, Hash: hashBytes},
		},
	}
	evts, err := mapFollowTipResponse(resp, false, 0)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.rollback", evts[0].Type)
}

// --------------- WatchTx: NativeBytes header extraction ---------------

func TestWatchTxApplyNativeBytesExtractsHeader(t *testing.T) {
	block, nativeBytes := decodeShelleyBlock(t)
	firstTx := block.Transactions()[0]

	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Apply{
			Apply: &watchpb.AnyChainTx{
				Chain: &watchpb.AnyChainTx_Cardano{
					Cardano: &cardanopb.Tx{
						Hash:   firstTx.Hash().Bytes(),
						Fee:    &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 200000}},
						Inputs: []*cardanopb.TxInput{{TxHash: []byte{0xaa}, OutputIndex: 0}},
					},
				},
				Block: &watchpb.AnyChainBlock{NativeBytes: nativeBytes},
			},
		},
	}

	evts, err := mapWatchTxResponse(resp, 764824073)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.transaction", evts[0].Type)

	txCtx := evts[0].Context.(event.TransactionContext)
	assert.Equal(t, block.SlotNumber(), txCtx.SlotNumber)
	assert.Equal(t, block.BlockNumber(), txCtx.BlockNumber)
}

// --------------- WatchTx: Protobuf path ---------------

func TestWatchTxApplyProtobufWithHeader(t *testing.T) {
	txHash := []byte{0xaa, 0xbb}
	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Apply{
			Apply: &watchpb.AnyChainTx{
				Chain: &watchpb.AnyChainTx_Cardano{
					Cardano: &cardanopb.Tx{
						Hash:   txHash,
						Fee:    &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 300000}},
						Inputs: []*cardanopb.TxInput{{TxHash: []byte{0x01}, OutputIndex: 1}},
					},
				},
				Block: &watchpb.AnyChainBlock{
					Chain: &watchpb.AnyChainBlock_Cardano{
						Cardano: &cardanopb.Block{
							Header: &cardanopb.BlockHeader{Slot: 200, Hash: []byte{0x02}, Height: 100},
						},
					},
				},
			},
		},
	}

	evts, err := mapWatchTxResponse(resp, 764824073)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.transaction", evts[0].Type)

	txCtx := evts[0].Context.(event.TransactionContext)
	assert.Equal(t, hex.EncodeToString(txHash), txCtx.TransactionHash)
	assert.Equal(t, uint64(200), txCtx.SlotNumber)
	assert.Equal(t, uint32(764824073), txCtx.NetworkMagic)

	txEvt := evts[0].Payload.(event.TransactionEvent)
	assert.Equal(t, uint64(300000), txEvt.Fee)
	assert.Len(t, txEvt.Inputs, 1)
}

func TestWatchTxApplyProtobufNoBlock(t *testing.T) {
	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Apply{
			Apply: &watchpb.AnyChainTx{
				Chain: &watchpb.AnyChainTx_Cardano{
					Cardano: &cardanopb.Tx{
						Hash: []byte{0xcc},
						Fee:  &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 180000}},
					},
				},
			},
		},
	}

	evts, err := mapWatchTxResponse(resp, 0)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.transaction", evts[0].Type)

	txEvt := evts[0].Payload.(event.TransactionEvent)
	assert.Equal(t, uint64(180000), txEvt.Fee)
}

func TestWatchTxApplyProtobufGovernance(t *testing.T) {
	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Apply{
			Apply: &watchpb.AnyChainTx{
				Chain: &watchpb.AnyChainTx_Cardano{
					Cardano: &cardanopb.Tx{
						Hash: []byte{0xdd},
						Proposals: []*cardanopb.GovernanceActionProposal{
							{GovAction: &cardanopb.GovernanceAction{
								GovernanceAction: &cardanopb.GovernanceAction_InfoAction{InfoAction: 6},
							}},
						},
					},
				},
			},
		},
	}

	evts, err := mapWatchTxResponse(resp, 0)
	require.NoError(t, err)
	require.Len(t, evts, 2, "tx + governance")
	assert.Equal(t, "input.transaction", evts[0].Type)
	assert.Equal(t, "input.governance", evts[1].Type)
}

// --------------- WatchTx: Undo / Idle ---------------

func TestWatchTxUndoEmitsRollback(t *testing.T) {
	hashBytes, _ := hex.DecodeString("ab")
	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Undo{
			Undo: &watchpb.AnyChainTx{
				Chain: &watchpb.AnyChainTx_Cardano{
					Cardano: &cardanopb.Tx{Hash: []byte{0xdd}},
				},
				Block: &watchpb.AnyChainBlock{
					Chain: &watchpb.AnyChainBlock_Cardano{
						Cardano: &cardanopb.Block{
							Header: &cardanopb.BlockHeader{Slot: 42, Hash: hashBytes},
						},
					},
				},
			},
		},
	}

	evts, err := mapWatchTxResponse(resp, 0)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.rollback", evts[0].Type)
}

func TestWatchTxUndoNativeBytesEmitsRollback(t *testing.T) {
	block, nativeBytes := decodeShelleyBlock(t)

	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Undo{
			Undo: &watchpb.AnyChainTx{
				Chain: &watchpb.AnyChainTx_Cardano{
					Cardano: &cardanopb.Tx{Hash: []byte{0xee}},
				},
				Block: &watchpb.AnyChainBlock{NativeBytes: nativeBytes},
			},
		},
	}

	evts, err := mapWatchTxResponse(resp, 0)
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, "input.rollback", evts[0].Type)

	rb := evts[0].Payload.(event.RollbackEvent)
	assert.Equal(t, block.SlotNumber(), rb.SlotNumber)
}

func TestWatchTxIdleProducesNoEvents(t *testing.T) {
	resp := &watchpb.WatchTxResponse{
		Action: &watchpb.WatchTxResponse_Idle{
			Idle: &watchpb.BlockRef{},
		},
	}

	evts, err := mapWatchTxResponse(resp, 0)
	require.NoError(t, err)
	assert.Len(t, evts, 0)
}
