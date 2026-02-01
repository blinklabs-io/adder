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
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/gouroboros/ledger/conway"
	"github.com/stretchr/testify/assert"
)

func TestExtractGovActionData(t *testing.T) {
	t.Run("extracts InfoActionData", func(t *testing.T) {
		action := &common.InfoGovAction{
			Type: 6,
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.Info)
		assert.Nil(t, result.ParameterChange)
		assert.Nil(t, result.TreasuryWithdrawal)
	})

	t.Run("extracts NoConfidenceActionData with prev action", func(t *testing.T) {
		txId := [32]byte{}
		copy(txId[:], []byte("prevtxhash12345678901234567890"))
		action := &common.NoConfidenceGovAction{
			Type: 3,
			ActionId: &common.GovActionId{
				TransactionId: txId,
				GovActionIdx:  5,
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.NoConfidence)
		assert.NotNil(t, result.NoConfidence.PrevActionId)
		assert.Equal(t, hex.EncodeToString(txId[:]), result.NoConfidence.PrevActionId.TransactionId)
		assert.Equal(t, uint32(5), result.NoConfidence.PrevActionId.GovActionIdx)
	})

	t.Run("extracts NoConfidenceActionData without prev action", func(t *testing.T) {
		action := &common.NoConfidenceGovAction{
			Type:     3,
			ActionId: nil,
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.NoConfidence)
		assert.Nil(t, result.NoConfidence.PrevActionId)
	})

	t.Run("extracts HardForkInitiationActionData", func(t *testing.T) {
		action := &common.HardForkInitiationGovAction{
			Type:     1,
			ActionId: nil,
			ProtocolVersion: struct {
				cbor.StructAsArray
				Major uint
				Minor uint
			}{Major: 10, Minor: 0},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.HardForkInitiation)
		assert.Equal(t, uint(10), result.HardForkInitiation.ProtocolVersion.Major)
		assert.Equal(t, uint(0), result.HardForkInitiation.ProtocolVersion.Minor)
	})
}

func TestExtractGovActionId(t *testing.T) {
	t.Run("extracts transaction ID and index", func(t *testing.T) {
		txId := [32]byte{}
		copy(txId[:], []byte("abcdefghijklmnopqrstuvwxyz123456"))
		govActionId := &common.GovActionId{
			TransactionId: txId,
			GovActionIdx:  42,
		}

		result := extractGovActionId(govActionId)

		assert.NotNil(t, result)
		assert.Equal(t, hex.EncodeToString(txId[:]), result.TransactionId)
		assert.Equal(t, uint32(42), result.GovActionIdx)
	})

	t.Run("returns nil for nil input", func(t *testing.T) {
		result := extractGovActionId(nil)
		assert.Nil(t, result)
	})
}

func TestExtractTreasuryWithdrawalAction(t *testing.T) {
	t.Run("extracts withdrawals with multiple destinations", func(t *testing.T) {
		addr1 := common.Address{}
		addr2 := common.Address{}

		action := &common.TreasuryWithdrawalGovAction{
			Type: 2,
			Withdrawals: map[*common.Address]uint64{
				&addr1: 1000000,
				&addr2: 2000000,
			},
			PolicyHash: []byte{0xab, 0xcd, 0xef},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.TreasuryWithdrawal)
		assert.Len(t, result.TreasuryWithdrawal.Withdrawals, 2)
		assert.Equal(t, "abcdef", result.TreasuryWithdrawal.PolicyHash)

		// Verify total amount matches (order may vary due to map iteration)
		var total uint64
		for _, w := range result.TreasuryWithdrawal.Withdrawals {
			total += w.Amount
		}
		assert.Equal(t, uint64(3000000), total)
	})

	t.Run("extracts withdrawals without policy hash", func(t *testing.T) {
		addr := common.Address{}

		action := &common.TreasuryWithdrawalGovAction{
			Type: 2,
			Withdrawals: map[*common.Address]uint64{
				&addr: 5000000,
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.TreasuryWithdrawal)
		assert.Len(t, result.TreasuryWithdrawal.Withdrawals, 1)
		assert.Equal(t, "", result.TreasuryWithdrawal.PolicyHash)
	})
}

func TestExtractUpdateCommitteeAction(t *testing.T) {
	t.Run("extracts committee members to add and remove", func(t *testing.T) {
		// Create credentials with proper Blake2b224 hashes
		var credHash1, credHash2, credHash3 common.Blake2b224
		copy(credHash1[:], []byte("credential1hash12345678"))
		copy(credHash2[:], []byte("credential2hash12345678"))
		copy(credHash3[:], []byte("credential3hash12345678"))

		cred1 := common.Credential{
			CredType:   common.CredentialTypeAddrKeyHash,
			Credential: credHash1,
		}
		cred2 := &common.Credential{
			CredType:   common.CredentialTypeAddrKeyHash,
			Credential: credHash2,
		}
		cred3 := &common.Credential{
			CredType:   common.CredentialTypeAddrKeyHash,
			Credential: credHash3,
		}

		action := &common.UpdateCommitteeGovAction{
			Type: 4,
			Credentials: []common.Credential{
				cred1,
			},
			CredEpochs: map[*common.Credential]uint{
				cred2: 500,
				cred3: 600,
			},
			Quorum: cbor.Rat{Rat: big.NewRat(2, 3)},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.UpdateCommittee)
		assert.Len(t, result.UpdateCommittee.MembersToRemove, 1)
		assert.Len(t, result.UpdateCommittee.MembersToAdd, 2)
		assert.Equal(t, uint64(2), result.UpdateCommittee.QuorumNumerator)
		assert.Equal(t, uint64(3), result.UpdateCommittee.QuorumDenominator)
	})

	t.Run("extracts with previous action ID", func(t *testing.T) {
		txId := [32]byte{}
		copy(txId[:], []byte("committeeactiontxhash12345678"))

		action := &common.UpdateCommitteeGovAction{
			Type: 4,
			ActionId: &common.GovActionId{
				TransactionId: txId,
				GovActionIdx:  3,
			},
			Quorum: cbor.Rat{Rat: big.NewRat(1, 2)},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.UpdateCommittee)
		assert.NotNil(t, result.UpdateCommittee.PrevActionId)
		assert.Equal(t, hex.EncodeToString(txId[:]), result.UpdateCommittee.PrevActionId.TransactionId)
		assert.Equal(t, uint32(3), result.UpdateCommittee.PrevActionId.GovActionIdx)
	})
}

func TestExtractNewConstitutionAction(t *testing.T) {
	t.Run("extracts constitution with anchor", func(t *testing.T) {
		dataHash := [32]byte{}
		copy(dataHash[:], []byte("constitutionhash123456789012"))

		action := &common.NewConstitutionGovAction{
			Type: 5,
			Constitution: struct {
				cbor.StructAsArray
				Anchor     common.GovAnchor
				ScriptHash []byte
			}{
				Anchor: common.GovAnchor{
					Url:      "https://example.com/constitution.json",
					DataHash: dataHash,
				},
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.NewConstitution)
		assert.Equal(t, "https://example.com/constitution.json", result.NewConstitution.Anchor.Url)
		assert.Equal(t, hex.EncodeToString(dataHash[:]), result.NewConstitution.Anchor.DataHash)
		assert.Equal(t, "", result.NewConstitution.ScriptHash)
	})

	t.Run("extracts constitution with script hash", func(t *testing.T) {
		dataHash := [32]byte{}
		copy(dataHash[:], []byte("constitutionhash123456789012"))

		action := &common.NewConstitutionGovAction{
			Type: 5,
			Constitution: struct {
				cbor.StructAsArray
				Anchor     common.GovAnchor
				ScriptHash []byte
			}{
				Anchor: common.GovAnchor{
					Url:      "https://example.com/constitution.json",
					DataHash: dataHash,
				},
				ScriptHash: []byte{0xde, 0xad, 0xbe, 0xef},
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.NewConstitution)
		assert.Equal(t, "deadbeef", result.NewConstitution.ScriptHash)
	})

	t.Run("extracts constitution with previous action ID", func(t *testing.T) {
		txId := [32]byte{}
		copy(txId[:], []byte("prevconstitutionactiontx12345"))
		dataHash := [32]byte{}

		action := &common.NewConstitutionGovAction{
			Type: 5,
			ActionId: &common.GovActionId{
				TransactionId: txId,
				GovActionIdx:  7,
			},
			Constitution: struct {
				cbor.StructAsArray
				Anchor     common.GovAnchor
				ScriptHash []byte
			}{
				Anchor: common.GovAnchor{
					Url:      "https://example.com/new-constitution.json",
					DataHash: dataHash,
				},
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.NewConstitution)
		assert.NotNil(t, result.NewConstitution.PrevActionId)
		assert.Equal(t, uint32(7), result.NewConstitution.PrevActionId.GovActionIdx)
	})
}

func TestExtractParameterChangeAction(t *testing.T) {
	t.Run("extracts parameter change with basic params", func(t *testing.T) {
		minFeeA := uint(44)
		minFeeB := uint(155381)
		maxTxSize := uint(16384)

		action := &conway.ConwayParameterChangeGovAction{
			Type: 0,
			ParamUpdate: conway.ConwayProtocolParameterUpdate{
				MinFeeA:   &minFeeA,
				MinFeeB:   &minFeeB,
				MaxTxSize: &maxTxSize,
			},
			PolicyHash: []byte{0xca, 0xfe, 0xba, 0xbe},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.ParameterChange)
		assert.Equal(t, "cafebabe", result.ParameterChange.PolicyHash)
		assert.NotNil(t, result.ParameterChange.ParamUpdate)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.MinFeeA)
		assert.Equal(t, uint(44), *result.ParameterChange.ParamUpdate.MinFeeA)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.MinFeeB)
		assert.Equal(t, uint(155381), *result.ParameterChange.ParamUpdate.MinFeeB)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.MaxTxSize)
		assert.Equal(t, uint(16384), *result.ParameterChange.ParamUpdate.MaxTxSize)
	})

	t.Run("extracts parameter change with previous action ID", func(t *testing.T) {
		txId := [32]byte{}
		copy(txId[:], []byte("paramchangeactiontxhash12345"))
		minFeeA := uint(50)

		action := &conway.ConwayParameterChangeGovAction{
			Type: 0,
			ActionId: &common.GovActionId{
				TransactionId: txId,
				GovActionIdx:  2,
			},
			ParamUpdate: conway.ConwayProtocolParameterUpdate{
				MinFeeA: &minFeeA,
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.ParameterChange)
		assert.NotNil(t, result.ParameterChange.PrevActionId)
		assert.Equal(t, hex.EncodeToString(txId[:]), result.ParameterChange.PrevActionId.TransactionId)
		assert.Equal(t, uint32(2), result.ParameterChange.PrevActionId.GovActionIdx)
	})

	t.Run("extracts parameter change with execution units", func(t *testing.T) {
		action := &conway.ConwayParameterChangeGovAction{
			Type: 0,
			ParamUpdate: conway.ConwayProtocolParameterUpdate{
				MaxTxExUnits: &common.ExUnits{
					Memory: 10000000,
					Steps:  10000000000,
				},
				MaxBlockExUnits: &common.ExUnits{
					Memory: 50000000,
					Steps:  40000000000,
				},
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.ParameterChange)
		assert.NotNil(t, result.ParameterChange.ParamUpdate)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.MaxTxExUnits)
		assert.Equal(t, int64(10000000), result.ParameterChange.ParamUpdate.MaxTxExUnits.Mem)
		assert.Equal(t, int64(10000000000), result.ParameterChange.ParamUpdate.MaxTxExUnits.Steps)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.MaxBlockExUnits)
		assert.Equal(t, int64(50000000), result.ParameterChange.ParamUpdate.MaxBlockExUnits.Mem)
		assert.Equal(t, int64(40000000000), result.ParameterChange.ParamUpdate.MaxBlockExUnits.Steps)
	})

	t.Run("extracts parameter change with rational values", func(t *testing.T) {
		action := &conway.ConwayParameterChangeGovAction{
			Type: 0,
			ParamUpdate: conway.ConwayProtocolParameterUpdate{
				A0:  &cbor.Rat{Rat: big.NewRat(1, 10)},   // 0.1
				Rho: &cbor.Rat{Rat: big.NewRat(3, 1000)}, // 0.003
				Tau: &cbor.Rat{Rat: big.NewRat(2, 10)},   // 0.2
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.ParameterChange)
		assert.NotNil(t, result.ParameterChange.ParamUpdate)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.A0)
		assert.InDelta(t, 0.1, *result.ParameterChange.ParamUpdate.A0, 0.0001)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.Rho)
		assert.InDelta(t, 0.003, *result.ParameterChange.ParamUpdate.Rho, 0.0001)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.Tau)
		assert.InDelta(t, 0.2, *result.ParameterChange.ParamUpdate.Tau, 0.0001)
	})

	t.Run("extracts parameter change with governance parameters", func(t *testing.T) {
		minCommitteeSize := uint(5)
		committeeTermLimit := uint64(146)
		govActionDeposit := uint64(100000000000)
		drepDeposit := uint64(500000000)

		action := &conway.ConwayParameterChangeGovAction{
			Type: 0,
			ParamUpdate: conway.ConwayProtocolParameterUpdate{
				MinCommitteeSize:   &minCommitteeSize,
				CommitteeTermLimit: &committeeTermLimit,
				GovActionDeposit:   &govActionDeposit,
				DRepDeposit:        &drepDeposit,
			},
		}

		result := extractGovActionData(action)

		assert.NotNil(t, result.ParameterChange)
		assert.NotNil(t, result.ParameterChange.ParamUpdate)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.MinCommitteeSize)
		assert.Equal(t, uint(5), *result.ParameterChange.ParamUpdate.MinCommitteeSize)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.CommitteeTermLimit)
		assert.Equal(t, uint64(146), *result.ParameterChange.ParamUpdate.CommitteeTermLimit)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.GovActionDeposit)
		assert.Equal(t, uint64(100000000000), *result.ParameterChange.ParamUpdate.GovActionDeposit)
		assert.NotNil(t, result.ParameterChange.ParamUpdate.DRepDeposit)
		assert.Equal(t, uint64(500000000), *result.ParameterChange.ParamUpdate.DRepDeposit)
	})
}

func TestRationalToFloat(t *testing.T) {
	t.Run("converts rational to float", func(t *testing.T) {
		// 1/2 = 0.5
		rat := &cbor.Rat{Rat: big.NewRat(1, 2)}

		result := rationalToFloat(rat)

		assert.NotNil(t, result)
		assert.InDelta(t, 0.5, *result, 0.0001)
	})

	t.Run("returns nil for nil input", func(t *testing.T) {
		result := rationalToFloat(nil)
		assert.Nil(t, result)
	})

	t.Run("converts complex rational", func(t *testing.T) {
		// 3/4 = 0.75
		rat := &cbor.Rat{Rat: big.NewRat(3, 4)}

		result := rationalToFloat(rat)

		assert.NotNil(t, result)
		assert.InDelta(t, 0.75, *result, 0.0001)
	})
}
