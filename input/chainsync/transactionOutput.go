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
	"github.com/blinklabs-io/gouroboros/base58"
	"github.com/blinklabs-io/gouroboros/bech32"
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	utxorpc "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
)

// ResolvedTransactionOutput represents a concrete implementation of the TransactionOutput interface
type ResolvedTransactionOutput struct {
	AddressField common.Address             `json:"address"`
	AmountField  uint64                     `json:"amount"`
	AssetsField  *common.MultiAsset[uint64] `json:"assets,omitempty"`
}

func ExtractAssetDetailsFromMatch(match kugo.Match) (common.MultiAsset[uint64], uint64, error) {
	// Initialize the map that will store the assets
	assetsMap := map[common.Blake2b224]map[cbor.ByteString]uint64{}
	totalLovelace := uint64(0)

	// Iterate over all policies (asset types) in the Value map
	for policyId, assets := range match.Value {
		// Decode policyId if not ADA
		policyIdBytes, err := hex.DecodeString(policyId)
		if err != nil {
			slog.Debug(fmt.Sprintf("PolicyId %s is not a valid hex string\n", policyId))
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
				slog.Debug(fmt.Sprintf("Found ADA (lovelace): %d\n", totalLovelace))
				continue // Skip adding "lovelace" to assetsMap, as it is handled separately
			}

			byteStringAssetName := cbor.NewByteString([]byte(assetName))
			assetAmount := amount.Uint64()
			policyAssets[byteStringAssetName] = assetAmount
			slog.Debug("Get policyId, assetName, assetAmount from match.Value")
			slog.Debug(fmt.Sprintf("policyId: %s, assetName: %s, amount: %d\n", policyId, assetName, assetAmount))
		}

		// Only add non-empty policyAssets to the assetsMap
		if len(policyAssets) > 0 {
			assetsMap[policyBlake] = policyAssets
		}
	}

	assets := common.NewMultiAsset(assetsMap)
	return assets, totalLovelace, nil
}

func NewResolvedTransactionOutput(match kugo.Match) (ledger.TransactionOutput, error) {
	// FIXME - This is a patch to fix the issue with the address
	// Attempt to create an address using Bech32
	addr, err := common.NewAddress(match.Address)
	if err != nil {
		// If Bech32 fails, try to convert from Base58 to Bech32
		bech32addr, err := ConvertBase58ToBech32(match.Address, "addr")
		if err != nil {
			return nil, fmt.Errorf("failed to convert base58 to bech32: %w", err)
		}

		// Try to create the address again with the converted Bech32 address
		addr, err = common.NewAddress(bech32addr)
		if err != nil {
			return nil, fmt.Errorf("failed to create address from base58-converted bech32 address: %w", err)
		}
	}

	assets, amount, err := ExtractAssetDetailsFromMatch(match)
	if err != nil {
		return nil, fmt.Errorf("failed to extract asset details from match: %w", err)
	}

	slog.Debug(fmt.Sprintf("ResolvedTransactionOutput: address: %s, amount: %d, assets: %#v\n", addr, amount, assets))
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

func (txOut ResolvedTransactionOutput) Datum() *cbor.LazyValue {
	// Placeholder for Datum serialization
	return nil
}

func (txOut ResolvedTransactionOutput) DatumHash() *common.Blake2b256 {
	// Placeholder for DatumHash serialization
	return nil
}

func (txOut ResolvedTransactionOutput) Cbor() []byte {
	// Placeholder for CBOR serialization
	return []byte{}
}

func (txOut ResolvedTransactionOutput) Utxorpc() *utxorpc.TxOutput {
	// Placeholder for UTXO RPC representation
	return &utxorpc.TxOutput{}
}

// ConvertBase58ToBech32 converts a Base58 string to a Bech32 string
// using the given human-readable part (hrp) required for Bech32 encoding
func ConvertBase58ToBech32(base58Str, hrp string) (string, error) {
	data := base58.Decode(base58Str)
	converted, err := bech32.ConvertBits(data, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("failed to convert bits: %w", err)
	}
	bech32Str, err := bech32.Encode(hrp, converted)
	if err != nil {
		return "", fmt.Errorf("failed to encode Bech32: %w", err)
	}

	return bech32Str, nil
}
