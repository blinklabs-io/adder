package chainsync

import (
	"encoding/hex"
	"fmt"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	utxorpc "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
)

// ResolvedTransactionOutput represents a concrete implementation of the TransactionOutput interface
type ResolvedTransactionOutput struct {
	address   common.Address
	amount    uint64
	assets    *common.MultiAsset[uint64]
	datum     *cbor.LazyValue
	datumHash *common.Blake2b256
}

func ExtractAssetDetailsFromMatch(match kugo.Match) (common.MultiAsset[uint64], uint64, error) {
	// Initialize the map that will store the assets
	assetsMap := map[common.Blake2b224]map[cbor.ByteString]uint64{}
	totalLovelace := uint64(0)

	// Iterate over all policies (asset types) in the Value map
	for policyId, assets := range match.Value {
		policyIdBytes, err := hex.DecodeString(policyId)
		if err != nil {
			fmt.Printf("PolicyId %s is not a valid hex string\n", policyId)
			policyIdBytes = []byte(policyId)
		}
		policyBlake := common.NewBlake2b224(policyIdBytes)

		policyAssets := make(map[cbor.ByteString]uint64)

		// Iterate over all assets within this policyId
		for assetName, amount := range assets {
			// Convert assetName from string to cbor.ByteString
			byteStringAssetName := cbor.NewByteString([]byte(assetName))

			// Convert num.Int to uint64
			assetAmount := amount.Uint64()

			// Add the asset amount to the policyAssets map
			policyAssets[byteStringAssetName] = assetAmount
			fmt.Println("Get policyId, assetName, assetAmount from match.Value")
			fmt.Printf("policyId: %s, assetName: %s, amount: %d\n", policyId, assetName, assetAmount)
			// TODO What is the amount if I have multiple assets?
		}
		assetsMap[policyBlake] = policyAssets
	}

	assets := common.NewMultiAsset(assetsMap)
	return assets, totalLovelace, nil
}

func NewResolvedTransactionOutput(match kugo.Match) (*ResolvedTransactionOutput, error) {
	addr, err := common.NewAddress(match.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to create address from match.Address: %w", err)
	}

	// Extract asset details and the amount from the match
	assets, amount, err := ExtractAssetDetailsFromMatch(match)
	if err != nil {
		return nil, fmt.Errorf("failed to extract asset details from match: %w", err)
	}

	fmt.Printf("ResolvedTransactionOutput: address: %s, amount: %d, assets: %#v\n", addr, amount, assets)
	return &ResolvedTransactionOutput{
		address: addr,
		amount:  amount,
		assets:  &assets,
	}, nil
}

// Implementation of the TransactionOutput interface methods
func (txOut ResolvedTransactionOutput) Address() common.Address {
	return txOut.address
}

func (txOut ResolvedTransactionOutput) Amount() uint64 {
	return txOut.amount
}

func (txOut ResolvedTransactionOutput) Assets() *common.MultiAsset[uint64] {
	return txOut.assets
}

func (txOut ResolvedTransactionOutput) Datum() *cbor.LazyValue {
	return txOut.datum
}

func (txOut ResolvedTransactionOutput) DatumHash() *common.Blake2b256 {
	return txOut.datumHash
}

func (txOut ResolvedTransactionOutput) Cbor() []byte {
	// TODO: Return CBOR-encoded output, placeholder for now
	return []byte{}
}

func (txOut ResolvedTransactionOutput) Utxorpc() *utxorpc.TxOutput {
	// TODO: Return example RPC representation, placeholder for now
	return &utxorpc.TxOutput{}
}
