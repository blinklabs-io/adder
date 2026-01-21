// Copyright 2025 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cardano

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/plutigo/data"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/stretchr/testify/assert"
	"github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"

	"github.com/blinklabs-io/adder/event"
)

// MockAddress is a mock implementation of the ledger.Address interface
type MockAddress struct {
	common.Address // Embed the common.Address struct
}

func (m MockAddress) ByronAttr() common.ByronAddressAttributes {
	return common.ByronAddressAttributes{}
}

func (m MockAddress) ByronType() uint64 {
	return 0
}

func (m MockAddress) Bytes() []byte {
	return []byte("mockAddressBytes")
}

func (m *MockAddress) MarshalCBOR() ([]byte, error) {
	return []byte{}, nil
}

func (m MockAddress) MarshalJSON() ([]byte, error) {
	return []byte("{}"), nil
}

func (m MockAddress) NetworkId() uint {
	return 1
}

func (m MockAddress) PaymentAddress() *common.Address {
	return &common.Address{}
}

func (m *MockAddress) PaymentKeyHash() common.Blake2b224 {
	return common.Blake2b224Hash([]byte("paymentKeyHash"))
}

func (m MockAddress) StakeAddress() *common.Address {
	return &common.Address{}
}

func (m *MockAddress) StakeKeyHash() common.Blake2b224 {
	return common.Blake2b224Hash([]byte("stakeKeyHash"))
}

func (m MockAddress) String() string {
	return hex.EncodeToString(m.Bytes())
}

func (m MockAddress) Type() uint8 {
	return 0
}

func (m *MockAddress) UnmarshalCBOR(_ []byte) error {
	return nil
}

// MockOutput is a mock implementation of the TransactionOutput interface
type MockOutput struct {
	address   ledger.Address
	scriptRef common.Script
	assets    *common.MultiAsset[common.MultiAssetTypeOutput]
	datum     *common.Datum
	amount    uint64
}

func (m MockOutput) Address() ledger.Address {
	return m.address
}

func (m MockOutput) Amount() *big.Int {
	return big.NewInt(int64(m.amount))
}

func (m MockOutput) Assets() *common.MultiAsset[common.MultiAssetTypeOutput] {
	return m.assets
}

func (m MockOutput) Datum() *common.Datum {
	return m.datum
}

func (m MockOutput) DatumHash() *common.Blake2b256 {
	return nil
}

func (m MockOutput) ScriptRef() common.Script {
	return m.scriptRef
}

func (m MockOutput) Cbor() []byte {
	return []byte{}
}

func (m MockOutput) Utxorpc() (*cardano.TxOutput, error) {
	return nil, nil
}

func (m MockOutput) ToPlutusData() data.PlutusData {
	return nil
}

func (m MockOutput) String() string {
	return "mockOutput"
}

func TestNewCardano(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatalf("expected non-nil Cardano instance")
	}
}

func TestCardano_Start(t *testing.T) {
	c := New()
	err := c.Start()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Additional checks can be added here
}

func TestCardano_Stop(t *testing.T) {
	c := New()
	err := c.Start()
	if err != nil {
		t.Fatalf("expected no error on start, got %v", err)
	}
	err = c.Stop()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCardano_InputChan(t *testing.T) {
	c := New()
	err := c.Start()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer c.Stop()
	if c.InputChan() == nil {
		t.Fatalf("expected non-nil inputChan after Start()")
	}
}

func TestCardano_OutputChan(t *testing.T) {
	c := New()
	err := c.Start()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer c.Stop()
	if c.OutputChan() == nil {
		t.Fatalf("expected non-nil outputChan after Start()")
	}
}

// Mock certificate implementations
type mockStakeDelegationCert struct {
	cborData []byte
	common.StakeDelegationCertificate
}

func (m *mockStakeDelegationCert) Cbor() []byte { return m.cborData }

type mockStakeDeregistrationCert struct {
	cborData []byte
	common.StakeDeregistrationCertificate
}

func (m *mockStakeDeregistrationCert) Cbor() []byte { return m.cborData }

func mockStakeCredentialValue(
	credType uint,
	hashBytes []byte,
) common.Credential {
	var credHash common.CredentialHash
	copy(credHash[:], hashBytes)
	return common.Credential{
		CredType:   credType,
		Credential: credHash,
	}
}

func mockAddress(_ string) common.Address {
	return common.Address{}
}

func TestFilterByAddress(t *testing.T) {
	cred := mockStakeCredentialValue(0, bytes.Repeat([]byte{1}, 28))
	credHash := cred.Hash()
	convData, _ := bech32.ConvertBits(credHash[:], 8, 5, true)
	testStakeAddress, _ := bech32.Encode("stake", convData)

	tests := []struct {
		outputAddr    common.Address
		cert          ledger.Certificate
		name          string
		filterAddress string
		shouldMatch   bool
	}{
		{
			name:          "Basic address match",
			filterAddress: "addr_test1qqjwq357",
			outputAddr:    mockAddress("addr_test1qqjwq357"),
			shouldMatch:   true,
		},

		{
			name:          "StakeDelegationCertificate match",
			filterAddress: testStakeAddress,
			outputAddr:    mockAddress("addr_doesnt_match"),
			cert: &common.StakeDelegationCertificate{
				StakeCredential: &cred,
			},
			shouldMatch: true,
		},
		{
			name:          "StakeDeregistrationCertificate match",
			filterAddress: testStakeAddress,
			outputAddr:    mockAddress("addr_doesnt_match"),
			cert: &common.StakeDeregistrationCertificate{
				StakeCredential: cred,
			},
			shouldMatch: true,
		},
		{
			name:          "No match",
			filterAddress: "stake_test1uzw2x9z6y3q4y5z6x7y8z9",
			outputAddr:    mockAddress("addr_doesnt_match"),
			shouldMatch:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create cardano instance with address filter
			cs := New(WithAddresses([]string{tt.filterAddress}))

			output := MockOutput{
				address: tt.outputAddr,
				amount:  1000000,
				assets:  nil,
				datum:   nil,
			}

			txEvent := event.TransactionEvent{
				Outputs:        []ledger.TransactionOutput{output},
				ResolvedInputs: []ledger.TransactionOutput{output},
			}

			if tt.cert != nil {
				txEvent.Certificates = []ledger.Certificate{tt.cert}
			}

			evt := event.Event{Payload: txEvent}

			err := cs.Start()
			assert.NoError(t, err)
			defer cs.Stop()

			cs.InputChan() <- evt

			if tt.shouldMatch {
				select {
				case filteredEvt := <-cs.OutputChan():
					assert.Equal(t, evt, filteredEvt)
				case <-time.After(1 * time.Second):
					t.Error("Expected event to pass filter but it didn't")
				}
			} else {
				select {
				case <-cs.OutputChan():
					t.Error("Expected event to be filtered out but it passed through")
				case <-time.After(100 * time.Millisecond):
					// Expected no event
				}
			}
		})
	}
}

func TestFilterByPolicyId(t *testing.T) {
	// Setup Cardano with policy ID filter
	filterPolicyId := "random_policy_id"
	policyIdHash := common.Blake2b224Hash([]byte(filterPolicyId))
	cs := New(WithPolicies([]string{policyIdHash.String()}))

	// Mock transaction event
	policyId := policyIdHash // Use the same hash as the filter

	// Create a new MultiAsset with pre-populated data
	assetsData := make(
		map[common.Blake2b224]map[cbor.ByteString]common.MultiAssetTypeOutput,
	)
	assetName := cbor.NewByteString([]byte("asset1"))
	assetsData[policyId] = map[cbor.ByteString]common.MultiAssetTypeOutput{
		assetName: big.NewInt(1), // Add asset with quantity 1
	}
	assets := common.NewMultiAsset(assetsData)

	output := MockOutput{
		address: ledger.Address{},
		amount:  1000000,
		assets:  &assets,
		datum:   nil,
	}
	evt := event.Event{
		Payload: event.TransactionEvent{
			Outputs:        []ledger.TransactionOutput{output},
			ResolvedInputs: []ledger.TransactionOutput{output},
		},
	}

	// Start the filter
	err := cs.Start()
	assert.NoError(t, err, "Cardano filter should start without error")
	defer cs.Stop()

	// Send event to input channel
	cs.InputChan() <- evt

	// Wait for the event to be processed
	select {
	case filteredEvt := <-cs.OutputChan():
		assert.Equal(
			t,
			evt,
			filteredEvt,
			"Filtered event should match the input event",
		)
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out waiting for filtered event")
	}
}

func TestFilterByAssetFingerprint(t *testing.T) {
	// Setup Cardano with asset fingerprint filter
	filterAssetFingerprint := "asset1e58wmplshqdkkq97tz02chq980456wgt35tfjr"
	cs := New(WithAssetFingerprints([]string{filterAssetFingerprint}))

	// Mock transaction event
	policyId := common.Blake2b224Hash([]byte("policy1"))

	// Create a new MultiAsset with pre-populated data
	assetsData := make(
		map[common.Blake2b224]map[cbor.ByteString]common.MultiAssetTypeOutput,
	)
	assetName := cbor.NewByteString([]byte("asset1"))
	assetsData[policyId] = map[cbor.ByteString]common.MultiAssetTypeOutput{
		assetName: big.NewInt(1), // Add asset with quantity 1
	}
	assets := common.NewMultiAsset(assetsData)

	output := MockOutput{
		address: ledger.Address{},
		amount:  1000000,
		assets:  &assets,
		datum:   nil,
	}
	evt := event.Event{
		Payload: event.TransactionEvent{
			Outputs:        []ledger.TransactionOutput{output},
			ResolvedInputs: []ledger.TransactionOutput{output},
		},
	}

	// Start the filter
	err := cs.Start()
	assert.NoError(t, err, "Cardano filter should start without error")
	defer cs.Stop()

	// Send event to input channel
	cs.InputChan() <- evt

	// Wait for the event to be processed
	select {
	case filteredEvt := <-cs.OutputChan():
		assert.Equal(
			t,
			evt,
			filteredEvt,
			"Filtered event should match the input event",
		)
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out waiting for filtered event")
	}
}

func TestFilterByPoolId(t *testing.T) {
	// Setup Cardano with pool ID filter using hex format
	// The cardano filter uses O(1) lookups with pre-computed hex/bech32 conversions
	poolHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef12345678"
	cs := New(WithPoolIds([]string{poolHex}))

	// Mock block event - IssuerVkey should match the hex pool ID
	evt := event.Event{
		Payload: event.BlockEvent{
			IssuerVkey: poolHex, // Match the filterPoolIds
		},
	}

	// Start the filter
	err := cs.Start()
	assert.NoError(t, err, "Cardano filter should start without error")
	defer cs.Stop()

	// Send event to input channel
	cs.InputChan() <- evt

	// Wait for the event to be processed
	select {
	case filteredEvt := <-cs.OutputChan():
		assert.Equal(
			t,
			evt,
			filteredEvt,
			"Filtered event should match the input event",
		)
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out waiting for filtered event")
	}
}
