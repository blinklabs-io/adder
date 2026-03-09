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
	"encoding/hex"
	"math/big"

	"github.com/blinklabs-io/adder/event"
	lcommon "github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/gouroboros/ledger/shelley"
	cardanopb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
	"google.golang.org/protobuf/encoding/protowire"
)

// Protobuf fallback helpers: used when NativeBytes is not available and we
// must construct adder events from the parsed protobuf fields.

func pbBlockContext(header *cardanopb.BlockHeader, networkMagic uint32) event.BlockContext {
	return event.BlockContext{
		BlockNumber:  header.GetHeight(),
		SlotNumber:   header.GetSlot(),
		NetworkMagic: networkMagic,
	}
}

func pbBlockEvent(block *cardanopb.Block) event.BlockEvent {
	evt := event.BlockEvent{
		BlockHash: hex.EncodeToString(block.GetHeader().GetHash()),
	}
	if body := block.GetBody(); body != nil {
		evt.TransactionCount = uint64(len(body.GetTx()))
	}
	return evt
}

func pbTransactionContext(
	header *cardanopb.BlockHeader,
	txHash []byte,
	txIdx uint32,
	networkMagic uint32,
) event.TransactionContext {
	return event.TransactionContext{
		TransactionHash: hex.EncodeToString(txHash),
		BlockNumber:     header.GetHeight(),
		SlotNumber:      header.GetSlot(),
		TransactionIdx:  txIdx,
		NetworkMagic:    networkMagic,
	}
}

func pbTransactionEvent(blockHash []byte, tx *cardanopb.Tx) event.TransactionEvent {
	evt := event.TransactionEvent{
		BlockHash: hex.EncodeToString(blockHash),
		Fee:       pbFee(tx),
		Inputs:    pbInputsToLedger(tx.GetInputs()),
		Outputs:   pbOutputsToLedger(tx.GetOutputs()),
	}
	if v := tx.GetValidity(); v != nil && v.GetTtl() > 0 {
		evt.TTL = v.GetTtl()
	}
	if refs := tx.GetReferenceInputs(); len(refs) > 0 {
		evt.ReferenceInputs = pbInputsToLedger(refs)
	}
	if w := pbWithdrawals(tx.GetWithdrawals()); len(w) > 0 {
		evt.Withdrawals = w
	}
	return evt
}

func pbGovernanceContext(
	header *cardanopb.BlockHeader,
	txHash []byte,
	txIdx uint32,
	networkMagic uint32,
) event.GovernanceContext {
	return event.GovernanceContext{
		TransactionHash: hex.EncodeToString(txHash),
		BlockNumber:     header.GetHeight(),
		SlotNumber:      header.GetSlot(),
		TransactionIdx:  txIdx,
		NetworkMagic:    networkMagic,
	}
}

func pbGovernanceEvent(blockHash []byte, tx *cardanopb.Tx) event.GovernanceEvent {
	evt := event.GovernanceEvent{
		BlockHash: hex.EncodeToString(blockHash),
	}
	for i, prop := range tx.GetProposals() {
		evt.ProposalProcedures = append(evt.ProposalProcedures, pbProposalProcedure(prop, uint32(i)))
	}
	return evt
}

func pbProposalProcedure(prop *cardanopb.GovernanceActionProposal, idx uint32) event.ProposalProcedureData {
	data := event.ProposalProcedureData{
		Index:      idx,
		ActionType: pbGovActionType(prop),
	}
	if prop.GetDeposit() != nil {
		data.Deposit = utxorpcBigIntToUint64(prop.GetDeposit())
	}
	return data
}

func pbGovActionType(prop *cardanopb.GovernanceActionProposal) string {
	ga := prop.GetGovAction()
	if ga == nil {
		return "Unknown"
	}
	switch ga.GetGovernanceAction().(type) {
	case *cardanopb.GovernanceAction_HardForkInitiationAction:
		return "HardForkInitiation"
	case *cardanopb.GovernanceAction_NewConstitutionAction:
		return "NewConstitution"
	case *cardanopb.GovernanceAction_NoConfidenceAction:
		return "NoConfidence"
	case *cardanopb.GovernanceAction_ParameterChangeAction:
		return "ParameterChange"
	case *cardanopb.GovernanceAction_TreasuryWithdrawalsAction:
		return "TreasuryWithdrawal"
	case *cardanopb.GovernanceAction_UpdateCommitteeAction:
		return "UpdateCommittee"
	case *cardanopb.GovernanceAction_InfoAction:
		return "Info"
	default:
		return "Unknown"
	}
}

// pbFee extracts the transaction fee from a protobuf Tx. It first tries the
// typed GetFee() accessor (BigInt message at field 9). If that returns nil —
// which happens when the server encodes the fee as a raw uint64 varint
// instead of a BigInt message — it falls back to scanning the protobuf
// unknown fields for field 9 as a varint.
const txFeeFieldNumber = 9

func pbFee(tx *cardanopb.Tx) uint64 {
	if fee := utxorpcBigIntToUint64(tx.GetFee()); fee > 0 {
		return fee
	}
	raw := tx.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		num, wtype, n := protowire.ConsumeTag(raw)
		if n < 0 {
			break
		}
		raw = raw[n:]
		switch wtype {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(raw)
			if vn < 0 {
				return 0
			}
			raw = raw[vn:]
			if num == txFeeFieldNumber {
				return v
			}
		case protowire.Fixed32Type:
			raw = raw[4:]
		case protowire.Fixed64Type:
			raw = raw[8:]
		case protowire.BytesType:
			_, bn := protowire.ConsumeBytes(raw)
			if bn < 0 {
				return 0
			}
			raw = raw[bn:]
		default:
			return 0
		}
	}
	return 0
}

// pbWithdrawals converts UTxO RPC protobuf withdrawals to the adder event format.
func pbWithdrawals(withdrawals []*cardanopb.Withdrawal) map[string]uint64 {
	if len(withdrawals) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(withdrawals))
	for _, w := range withdrawals {
		acct := w.GetRewardAccount()
		if len(acct) == 0 {
			continue
		}
		addr, err := lcommon.NewAddressFromBytes(acct)
		if err != nil {
			continue
		}
		out[addr.String()] = utxorpcBigIntToUint64(w.GetCoin())
	}
	return out
}

// pbInputsToLedger converts UTxO RPC protobuf inputs to gouroboros ledger inputs.
func pbInputsToLedger(inputs []*cardanopb.TxInput) []lcommon.TransactionInput {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]lcommon.TransactionInput, len(inputs))
	for i, inp := range inputs {
		out[i] = shelley.ShelleyTransactionInput{
			TxId:        lcommon.NewBlake2b256(inp.GetTxHash()),
			OutputIndex: inp.GetOutputIndex(),
		}
	}
	return out
}

// pbOutputsToLedger converts UTxO RPC protobuf outputs to gouroboros ledger outputs.
func pbOutputsToLedger(outputs []*cardanopb.TxOutput) []lcommon.TransactionOutput {
	if len(outputs) == 0 {
		return nil
	}
	out := make([]lcommon.TransactionOutput, len(outputs))
	for i, o := range outputs {
		txOut := &shelley.ShelleyTransactionOutput{
			OutputAmount: utxorpcBigIntToUint64(o.GetCoin()),
		}
		if addrBytes := o.GetAddress(); len(addrBytes) > 0 {
			addr, err := lcommon.NewAddressFromBytes(addrBytes)
			if err == nil {
				txOut.OutputAddress = addr
			}
		}
		out[i] = txOut
	}
	return out
}

func utxorpcBigIntToUint64(b *cardanopb.BigInt) uint64 {
	if b == nil {
		return 0
	}
	if v := b.GetInt(); v > 0 {
		return uint64(v)
	}
	if raw := b.GetBigUInt(); len(raw) > 0 {
		return new(big.Int).SetBytes(raw).Uint64()
	}
	return 0
}
