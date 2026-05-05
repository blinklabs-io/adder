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
	"math"
	"math/big"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
	lcommon "github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/gouroboros/ledger/mary"
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
	inputs := pbInputsToLedger(tx.GetInputs())
	outputs := pbOutputsToLedger(tx.GetOutputs())
	refInputs := pbInputsToLedger(tx.GetReferenceInputs())
	poolCerts := pbPoolCertificatesToLedger(tx.GetCertificates())

	ttl := uint64(0)
	if v := tx.GetValidity(); v != nil {
		ttl = v.GetTtl()
	}

	evt := event.TransactionEvent{
		// Transaction is populated so that filter/cardano matchers (matchDRepFilterTx,
		// matchPoolFilterTx, etc.) are not silently skipped by their te.Transaction != nil
		// guards. VotingProcedures() always returns nil here because utxorpc v1alpha does
		// not expose votes in cardanopb.Tx; this will be populated once go-codegen
		// upgrades to a version that includes them (currently available in v1beta).
		Transaction:     pbBuildTransaction(tx, inputs, outputs, refInputs, poolCerts, ttl),
		BlockHash:       hex.EncodeToString(blockHash),
		Fee:             pbFee(tx),
		Inputs:          inputs,
		Outputs:         outputs,
		Certificates:    poolCerts,
		TTL:             ttl,
	}
	if len(refInputs) > 0 {
		evt.ReferenceInputs = refInputs
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
		//nolint:gosec // proposals per tx bounded by Cardano protocol
		evt.ProposalProcedures = append(evt.ProposalProcedures, pbProposalProcedure(prop, uint32(i)))
	}
	drep, voteDel, committee := pbGovernanceCertificates(tx.GetCertificates())
	evt.DRepCertificates = drep
	evt.VoteDelegationCertificates = voteDel
	evt.CommitteeCertificates = committee
	return evt
}

func pbProposalProcedure(prop *cardanopb.GovernanceActionProposal, idx uint32) event.ProposalProcedureData {
	data := event.ProposalProcedureData{
		Index:      idx,
		ActionType: pbGovActionType(prop),
		ActionData: pbGovActionData(prop),
		Anchor:     pbAnchor(prop.GetAnchor()),
	}
	if prop.GetDeposit() != nil {
		data.Deposit = utxorpcBigIntToUint64(prop.GetDeposit())
	}
	if acct := prop.GetRewardAccount(); len(acct) > 0 {
		if addr, err := lcommon.NewAddressFromBytes(acct); err == nil {
			data.RewardAccount = addr.String()
		}
	}
	return data
}

func pbAnchor(a *cardanopb.Anchor) event.AnchorData {
	if a == nil {
		return event.AnchorData{}
	}
	return event.AnchorData{
		Url:      a.GetUrl(),
		DataHash: hex.EncodeToString(a.GetContentHash()),
	}
}

func pbGovActionId(id *cardanopb.GovernanceActionId) *event.GovActionIdData {
	if id == nil {
		return nil
	}
	return &event.GovActionIdData{
		TransactionId: hex.EncodeToString(id.GetTransactionId()),
		GovActionIdx:  id.GetGovernanceActionIndex(),
	}
}

func pbStakeCredentialHex(cred *cardanopb.StakeCredential) string {
	if cred == nil {
		return ""
	}
	inner := cred.GetStakeCredential()
	if inner == nil {
		return ""
	}
	switch c := inner.(type) {
	case *cardanopb.StakeCredential_AddrKeyHash:
		return hex.EncodeToString(c.AddrKeyHash)
	case *cardanopb.StakeCredential_ScriptHash:
		return hex.EncodeToString(c.ScriptHash)
	}
	return ""
}

func pbGovActionData(prop *cardanopb.GovernanceActionProposal) event.GovActionData {
	ga := prop.GetGovAction()
	if ga == nil {
		return event.GovActionData{}
	}
	inner := ga.GetGovernanceAction()
	if inner == nil {
		return event.GovActionData{}
	}
	var data event.GovActionData
	switch a := inner.(type) {
	case *cardanopb.GovernanceAction_InfoAction:
		data.Info = &event.InfoActionData{}
	case *cardanopb.GovernanceAction_NoConfidenceAction:
		data.NoConfidence = &event.NoConfidenceActionData{
			PrevActionId: pbGovActionId(a.NoConfidenceAction.GetGovActionId()),
		}
	case *cardanopb.GovernanceAction_HardForkInitiationAction:
		d := &event.HardForkInitiationActionData{
			PrevActionId: pbGovActionId(a.HardForkInitiationAction.GetGovActionId()),
		}
		if pv := a.HardForkInitiationAction.GetProtocolVersion(); pv != nil {
			d.ProtocolVersion = event.ProtocolVersion{
				Major: uint(pv.GetMajor()),
				Minor: uint(pv.GetMinor()),
			}
		}
		data.HardForkInitiation = d
	case *cardanopb.GovernanceAction_TreasuryWithdrawalsAction:
		twa := a.TreasuryWithdrawalsAction
		d := &event.TreasuryWithdrawalActionData{
			PolicyHash: hex.EncodeToString(twa.GetPolicyHash()),
		}
		for _, w := range twa.GetWithdrawals() {
			addrStr := ""
			if addr, err := lcommon.NewAddressFromBytes(w.GetRewardAccount()); err == nil {
				addrStr = addr.String()
			}
			d.Withdrawals = append(d.Withdrawals, event.TreasuryWithdrawalItem{
				Address: addrStr,
				Amount:  utxorpcBigIntToUint64(w.GetCoin()),
			})
		}
		data.TreasuryWithdrawal = d
	case *cardanopb.GovernanceAction_UpdateCommitteeAction:
		uca := a.UpdateCommitteeAction
		d := &event.UpdateCommitteeActionData{
			PrevActionId: pbGovActionId(uca.GetGovActionId()),
		}
		for _, cred := range uca.GetRemoveCommitteeCredentials() {
			d.MembersToRemove = append(d.MembersToRemove, pbStakeCredentialHex(cred))
		}
		for _, nc := range uca.GetNewCommitteeCredentials() {
			d.MembersToAdd = append(d.MembersToAdd, event.CommitteeMember{
				Credential: pbStakeCredentialHex(nc.GetCommitteeColdCredential()),
				Epoch:      uint(nc.GetExpiresEpoch()),
			})
		}
		if qt := uca.GetNewCommitteeThreshold(); qt != nil {
			if n := qt.GetNumerator(); n > 0 {
				d.QuorumNumerator = uint64(n)
			}
			if dn := qt.GetDenominator(); dn > 0 {
				d.QuorumDenominator = uint64(dn)
			}
		}
		data.UpdateCommittee = d
	case *cardanopb.GovernanceAction_NewConstitutionAction:
		nca := a.NewConstitutionAction
		d := &event.NewConstitutionActionData{
			PrevActionId: pbGovActionId(nca.GetGovActionId()),
		}
		if c := nca.GetConstitution(); c != nil {
			d.Anchor = pbAnchor(c.GetAnchor())
			if h := c.GetHash(); len(h) > 0 {
				d.ScriptHash = hex.EncodeToString(h)
			}
		}
		data.NewConstitution = d
	case *cardanopb.GovernanceAction_ParameterChangeAction:
		pca := a.ParameterChangeAction
		d := &event.ParameterChangeActionData{
			PrevActionId: pbGovActionId(pca.GetGovActionId()),
			PolicyHash:   hex.EncodeToString(pca.GetPolicyHash()),
		}
		data.ParameterChange = d
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
		switch wtype { //nolint:exhaustive // only wire types relevant to scanning
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

// pbBuildTransaction constructs a ledger.Transaction from available protobuf
// fields so that filter/cardano matchers receive a non-nil Transaction.
func pbBuildTransaction(
	tx *cardanopb.Tx,
	inputs []lcommon.TransactionInput,
	outputs []lcommon.TransactionOutput,
	refInputs []lcommon.TransactionInput,
	certs []lcommon.Certificate,
	ttl uint64,
) ledger.Transaction {
	fee := new(big.Int).SetUint64(pbFee(tx))
	txHash := lcommon.NewBlake2b256(tx.GetHash())
	return &pbTransaction{
		txHash:    txHash,
		inputs:    inputs,
		outputs:   outputs,
		refInputs: refInputs,
		certs:     certs,
		fee:       fee,
		ttl:       ttl,
		isValid:   tx.GetSuccessful(),
	}
}

// pbTransaction implements ledger.Transaction using available protobuf data.
// VotingProcedures always returns nil because the utxorpc v1alpha schema does
// not expose votes in cardanopb.Tx; update once go-codegen provides them.
type pbTransaction struct {
	lcommon.TransactionBodyBase
	txHash    lcommon.Blake2b256
	inputs    []lcommon.TransactionInput
	outputs   []lcommon.TransactionOutput
	refInputs []lcommon.TransactionInput
	certs     []lcommon.Certificate
	fee       *big.Int
	ttl       uint64
	isValid   bool
}

func (t *pbTransaction) Id() lcommon.Blake2b256                   { return t.txHash }
func (t *pbTransaction) Inputs() []lcommon.TransactionInput       { return t.inputs }
func (t *pbTransaction) Outputs() []lcommon.TransactionOutput     { return t.outputs }
func (t *pbTransaction) ReferenceInputs() []lcommon.TransactionInput { return t.refInputs }
func (t *pbTransaction) Certificates() []lcommon.Certificate      { return t.certs }
func (t *pbTransaction) Fee() *big.Int                            { return t.fee }
func (t *pbTransaction) TTL() uint64                              { return t.ttl }

// Transaction-level methods (not TransactionBody).
func (t *pbTransaction) Type() int                                          { return 0 }
func (t *pbTransaction) Cbor() []byte                                       { return nil }
func (t *pbTransaction) Hash() lcommon.Blake2b256                           { return t.txHash }
func (t *pbTransaction) LeiosHash() lcommon.Blake2b256                      { return t.txHash }
func (t *pbTransaction) Metadata() lcommon.TransactionMetadatum             { return nil }
func (t *pbTransaction) AuxiliaryData() lcommon.AuxiliaryData               { return nil }
func (t *pbTransaction) IsValid() bool                                      { return t.isValid }
func (t *pbTransaction) Consumed() []lcommon.TransactionInput               { return t.inputs }
func (t *pbTransaction) Produced() []lcommon.Utxo                           { return nil }
func (t *pbTransaction) Witnesses() lcommon.TransactionWitnessSet           { return nil }
func (t *pbTransaction) ProtocolParameterUpdates() (uint64, map[lcommon.Blake2b224]lcommon.ProtocolParameterUpdate) {
	return 0, nil
}

// pbPoolCertificatesToLedger converts pool-related protobuf certificates to
// gouroboros ledger Certificate types for use in event.TransactionEvent.Certificates.
// Only StakeDelegation, PoolRetirement, and PoolRegistration are mapped here;
// governance certificates are handled separately via pbGovernanceCertificates.
func pbPoolCertificatesToLedger(certs []*cardanopb.Certificate) []lcommon.Certificate {
	var out []lcommon.Certificate
	for _, cert := range certs {
		if cert == nil {
			continue
		}
		switch c := cert.GetCertificate().(type) {
		case *cardanopb.Certificate_StakeDelegation:
			d := c.StakeDelegation
			poolHash := lcommon.NewBlake2b224(d.GetPoolKeyhash())
			out = append(out, &lcommon.StakeDelegationCertificate{
				PoolKeyHash: poolHash,
			})
		case *cardanopb.Certificate_PoolRetirement:
			d := c.PoolRetirement
			poolHash := lcommon.NewBlake2b224(d.GetPoolKeyhash())
			out = append(out, &lcommon.PoolRetirementCertificate{
				PoolKeyHash: poolHash,
				Epoch:       d.GetEpoch(),
			})
		case *cardanopb.Certificate_PoolRegistration:
			d := c.PoolRegistration
			operatorHash := lcommon.NewBlake2b224(d.GetOperator())
			out = append(out, &lcommon.PoolRegistrationCertificate{
				Operator: operatorHash,
			})
		}
	}
	return out
}

// pbMultiAssetsToLedger converts UTxO RPC protobuf multi-asset data to the
// gouroboros MultiAsset type used by output.Assets().
func pbMultiAssetsToLedger(assets []*cardanopb.Multiasset) *lcommon.MultiAsset[lcommon.MultiAssetTypeOutput] {
	data := make(map[lcommon.Blake2b224]map[cbor.ByteString]lcommon.MultiAssetTypeOutput)
	for _, ma := range assets {
		if len(ma.GetPolicyId()) == 0 {
			continue
		}
		policyId := lcommon.NewBlake2b224(ma.GetPolicyId())
		assetMap, ok := data[policyId]
		if !ok {
			assetMap = make(map[cbor.ByteString]lcommon.MultiAssetTypeOutput)
			data[policyId] = assetMap
		}
		for _, asset := range ma.GetAssets() {
			name := cbor.NewByteString(asset.GetName())
			assetMap[name] = new(big.Int).SetUint64(utxorpcBigIntToUint64(asset.GetOutputCoin()))
		}
	}
	ma := lcommon.NewMultiAsset(data)
	return &ma
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
// Outputs with multi-asset data use MaryTransactionOutput so that output.Assets()
// returns the correct policy/asset information for filter/cardano matchers.
func pbOutputsToLedger(outputs []*cardanopb.TxOutput) []lcommon.TransactionOutput {
	if len(outputs) == 0 {
		return nil
	}
	out := make([]lcommon.TransactionOutput, len(outputs))
	for i, o := range outputs {
		coin := utxorpcBigIntToUint64(o.GetCoin())
		var addr lcommon.Address
		if addrBytes := o.GetAddress(); len(addrBytes) > 0 {
			if a, err := lcommon.NewAddressFromBytes(addrBytes); err == nil {
				addr = a
			}
		}
		if pbAssets := o.GetAssets(); len(pbAssets) > 0 {
			out[i] = &mary.MaryTransactionOutput{
				OutputAddress: addr,
				OutputAmount: mary.MaryTransactionOutputValue{
					Amount: coin,
					Assets: pbMultiAssetsToLedger(pbAssets),
				},
			}
		} else {
			out[i] = &shelley.ShelleyTransactionOutput{
				OutputAddress: addr,
				OutputAmount:  coin,
			}
		}
	}
	return out
}

// pbGovernanceCertificates extracts DRep, vote-delegation, and committee
// certificate data from protobuf certificates.
func pbGovernanceCertificates(certs []*cardanopb.Certificate) (
	drep []event.DRepCertificateData,
	voteDel []event.VoteDelegationCertificateData,
	committee []event.CommitteeCertificateData,
) {
	for _, cert := range certs {
		inner := cert.GetCertificate()
		if inner == nil {
			continue
		}
		switch c := inner.(type) {
		case *cardanopb.Certificate_RegDrepCert:
			d := c.RegDrepCert
			drep = append(drep, event.DRepCertificateData{
				CertificateType: "Registration",
				DRepHash:        pbStakeCredentialHex(d.GetDrepCredential()),
				Deposit:         safeUint64ToInt64(utxorpcBigIntToUint64(d.GetCoin())),
				Anchor:          pbAnchor(d.GetAnchor()),
			})
		case *cardanopb.Certificate_UnregDrepCert:
			d := c.UnregDrepCert
			drep = append(drep, event.DRepCertificateData{
				CertificateType: "Deregistration",
				DRepHash:        pbStakeCredentialHex(d.GetDrepCredential()),
				Deposit:         safeUint64ToInt64(utxorpcBigIntToUint64(d.GetCoin())),
			})
		case *cardanopb.Certificate_UpdateDrepCert:
			d := c.UpdateDrepCert
			drep = append(drep, event.DRepCertificateData{
				CertificateType: "Update",
				DRepHash:        pbStakeCredentialHex(d.GetDrepCredential()),
				Anchor:          pbAnchor(d.GetAnchor()),
			})

		case *cardanopb.Certificate_VoteDelegCert:
			d := c.VoteDelegCert
			voteDel = append(voteDel, pbVoteDelegation("VoteDelegation", d.GetStakeCredential(), d.GetDrep(), nil, 0))
		case *cardanopb.Certificate_StakeVoteDelegCert:
			d := c.StakeVoteDelegCert
			voteDel = append(voteDel, pbVoteDelegation("StakeVoteDelegation", d.GetStakeCredential(), d.GetDrep(), d.GetPoolKeyhash(), 0))
		case *cardanopb.Certificate_VoteRegDelegCert:
			d := c.VoteRegDelegCert
			voteDel = append(voteDel, pbVoteDelegation("VoteRegistrationDelegation", d.GetStakeCredential(), d.GetDrep(), nil, safeUint64ToInt64(utxorpcBigIntToUint64(d.GetCoin()))))
		case *cardanopb.Certificate_StakeVoteRegDelegCert:
			d := c.StakeVoteRegDelegCert
			voteDel = append(voteDel, pbVoteDelegation("StakeVoteRegistrationDelegation", d.GetStakeCredential(), d.GetDrep(), d.GetPoolKeyhash(), safeUint64ToInt64(utxorpcBigIntToUint64(d.GetCoin()))))

		case *cardanopb.Certificate_AuthCommitteeHotCert:
			d := c.AuthCommitteeHotCert
			committee = append(committee, event.CommitteeCertificateData{
				CertificateType: "AuthHot",
				ColdCredential:  pbStakeCredentialHex(d.GetCommitteeColdCredential()),
				HotCredential:   pbStakeCredentialHex(d.GetCommitteeHotCredential()),
			})
		case *cardanopb.Certificate_ResignCommitteeColdCert:
			d := c.ResignCommitteeColdCert
			committee = append(committee, event.CommitteeCertificateData{
				CertificateType: "ResignCold",
				ColdCredential:  pbStakeCredentialHex(d.GetCommitteeColdCredential()),
				Anchor:          pbAnchor(d.GetAnchor()),
			})
		}
	}
	return drep, voteDel, committee
}

func pbVoteDelegation(
	certType string,
	stakeCred *cardanopb.StakeCredential,
	drep *cardanopb.DRep,
	poolKeyhash []byte,
	deposit int64,
) event.VoteDelegationCertificateData {
	data := event.VoteDelegationCertificateData{
		CertificateType: certType,
		StakeCredential: pbStakeCredentialHex(stakeCred),
		Deposit:         deposit,
	}
	if len(poolKeyhash) > 0 {
		data.PoolKeyHash = hex.EncodeToString(poolKeyhash)
	}
	if drep != nil {
		drepInner := drep.GetDrep()
		if drepInner == nil {
			return data
		}
		switch d := drepInner.(type) {
		case *cardanopb.DRep_AddrKeyHash:
			data.DRepType = "AddrKeyHash"
			data.DRepHash = hex.EncodeToString(d.AddrKeyHash)
		case *cardanopb.DRep_ScriptHash:
			data.DRepType = "ScriptHash"
			data.DRepHash = hex.EncodeToString(d.ScriptHash)
		case *cardanopb.DRep_Abstain:
			data.DRepType = "Abstain"
		case *cardanopb.DRep_NoConfidence:
			data.DRepType = "NoConfidence"
		}
	}
	return data
}

func safeUint64ToInt64(v uint64) int64 {
	if v > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v) //nolint:gosec // clamped above
}

func utxorpcBigIntToUint64(b *cardanopb.BigInt) uint64 {
	if b == nil {
		return 0
	}
	if v := b.GetInt(); v > 0 {
		return uint64(v)
	}
	if raw := b.GetBigUInt(); len(raw) > 0 {
		bi := new(big.Int).SetBytes(raw)
		if !bi.IsUint64() {
			return math.MaxUint64
		}
		return bi.Uint64()
	}
	return 0
}
