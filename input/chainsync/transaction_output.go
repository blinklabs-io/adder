// Copyright 2024 Blink Labs Software
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
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/plutigo/data"
	utxorpc "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
)

// ResolvedTransactionOutput represents a concrete implementation of the TransactionOutput interface
type ResolvedTransactionOutput struct {
	AddressField common.Address             `json:"address"`
	AmountField  uint64                     `json:"amount"`
	AssetsField  *common.MultiAsset[uint64] `json:"assets,omitempty"`
}

func ExtractAssetDetailsFromMatch(
	match kugo.Match,
) (common.MultiAsset[uint64], uint64, error) {
	// Initialize the map that will store the assets
	assetsMap := map[common.Blake2b224]map[cbor.ByteString]uint64{}
	totalLovelace := uint64(0)

	// Iterate over all policies (asset types) in the Value map
	for policyId, assets := range match.Value {
		// Decode policyId if not ADA
		policyIdBytes, err := hex.DecodeString(policyId)
		if err != nil {
			slog.Debug(
				fmt.Sprintf(
					"PolicyId %s is not a valid hex string\n",
					policyId,
				),
			)
			policyIdBytes = []byte(policyId)
		}
		policyBlake := common.NewBlake2b224(policyIdBytes)

		// Prepare the map for this policy's assets
		policyAssets := make(map[cbor.ByteString]uint64)

		// Iterate over all assets within this policyId
		for assetName, amount := range assets {
			// Check if this is the ADA (lovelace) asset
			if policyId == "ada" && assetName == "lovelace" {
				totalLovelace = amount.Uint64()
				slog.Debug(
					fmt.Sprintf("Found ADA (lovelace): %d\n", totalLovelace),
				)
				continue // Skip adding "lovelace" to assetsMap, as it is handled separately
			}

			byteStringAssetName := cbor.NewByteString([]byte(assetName))
			assetAmount := amount.Uint64()
			policyAssets[byteStringAssetName] = assetAmount
			slog.Debug("Get policyId, assetName, assetAmount from match.Value")
			slog.Debug(
				fmt.Sprintf(
					"policyId: %s, assetName: %s, amount: %d\n",
					policyId,
					assetName,
					assetAmount,
				),
			)
		}

		// Only add non-empty policyAssets to the assetsMap
		if len(policyAssets) > 0 {
			assetsMap[policyBlake] = policyAssets
		}
	}

	assets := common.NewMultiAsset(assetsMap)
	return assets, totalLovelace, nil
}

func NewResolvedTransactionOutput(
	match kugo.Match,
) (ledger.TransactionOutput, error) {
	// Get common.Address from base58 or bech32 string
	addr, err := common.NewAddress(match.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to convert base58 to bech32: %w", err)
	}

	assets, amount, err := ExtractAssetDetailsFromMatch(match)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to extract asset details from match: %w",
			err,
		)
	}

	slog.Debug(
		fmt.Sprintf(
			"ResolvedTransactionOutput: address: %s, amount: %d, assets: %#v\n",
			addr,
			amount,
			assets,
		),
	)
	return &ResolvedTransactionOutput{
		AddressField: addr,
		AmountField:  amount,
		// return assets if there are any, otherwise return nil
		AssetsField: func() *common.MultiAsset[uint64] {
			if len(assets.Policies()) > 0 {
				return &assets
			}
			return nil
		}(),
	}, nil
}

func (txOut ResolvedTransactionOutput) Address() common.Address {
	return txOut.AddressField
}

func (txOut ResolvedTransactionOutput) Amount() uint64 {
	return txOut.AmountField
}

func (txOut ResolvedTransactionOutput) Assets() *common.MultiAsset[uint64] {
	return txOut.AssetsField
}

func (txOut ResolvedTransactionOutput) Datum() *common.Datum {
	// Placeholder for Datum serialization
	return nil
}

func (txOut ResolvedTransactionOutput) DatumHash() *common.Blake2b256 {
	// Placeholder for DatumHash serialization
	return nil
}

func (txOut ResolvedTransactionOutput) ScriptRef() common.Script {
	// Placeholder for script ref
	return nil
}

func (txOut ResolvedTransactionOutput) Cbor() []byte {
	// Placeholder for CBOR serialization
	return []byte{}
}

func (txOut ResolvedTransactionOutput) Utxorpc() (*utxorpc.TxOutput, error) {
	// Placeholder for UTXO RPC representation
	return &utxorpc.TxOutput{}, nil
}

func (txOut ResolvedTransactionOutput) ToPlutusData() data.PlutusData {
	// Placeholder for PlutusData representation
	return nil
}
