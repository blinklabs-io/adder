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
