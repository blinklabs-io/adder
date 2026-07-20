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
	"testing"
	"time"

	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	mockledger "github.com/blinklabs-io/ouroboros-mock/ledger"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blinklabs-io/adder/event"
)

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

func TestFilterByAddress(t *testing.T) {
	cred := mockStakeCredentialValue(0, bytes.Repeat([]byte{1}, 28))
	credHash := cred.Hash()
	convData, _ := bech32.ConvertBits(credHash[:], 8, 5, true)
	testStakeAddress, _ := bech32.Encode("stake", convData)

	// matchAddress is a real testnet base address.
	const matchAddress = "addr_test1qz2fxv2umyhttkxyxp8x0dlpdt3k6cwng5pxj3jhsydzer3jcu5d8ps7zex2k2xt3uqxgjqnnj83ws8lhrn648jjxtwq2ytjqp"
	matchOutput, err := mockledger.NewTransactionOutputBuilder().
		WithLovelace(1000000).
		WithAddress(matchAddress).
		Build()
	require.NoError(t, err, "building mock output with match address")

	// placeholderOutput carries a default/empty address. These cases match (or
	// not) via certificate or filter mismatch, so the address is not asserted.
	placeholderOutput, err := mockledger.NewTransactionOutputBuilder().
		WithLovelace(1000000).
		Build()
	require.NoError(t, err, "building placeholder mock output")

	tests := []struct {
		output        ledger.TransactionOutput
		cert          ledger.Certificate
		name          string
		filterAddress string
		shouldMatch   bool
	}{
		{
			name:          "Basic address match",
			filterAddress: matchAddress,
			output:        matchOutput,
			shouldMatch:   true,
		},
		{
			name:          "StakeDelegationCertificate match",
			filterAddress: testStakeAddress,
			output:        placeholderOutput,
			cert: &common.StakeDelegationCertificate{
				StakeCredential: &cred,
			},
			shouldMatch: true,
		},
		{
			name:          "StakeDeregistrationCertificate match",
			filterAddress: testStakeAddress,
			output:        placeholderOutput,
			cert: &common.StakeDeregistrationCertificate{
				StakeCredential: cred,
			},
			shouldMatch: true,
		},
		{
			name:          "No match",
			filterAddress: "stake_test1uzw2x9z6y3q4y5z6x7y8z9",
			output:        placeholderOutput,
			shouldMatch:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create cardano instance with address filter
			cs := New(WithAddresses([]string{tt.filterAddress}))

			txEvent := event.TransactionEvent{
				Outputs:        []ledger.TransactionOutput{tt.output},
				ResolvedInputs: []ledger.TransactionOutput{tt.output},
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

	// Build a transaction output carrying the matching policy ID using the
	// shared ouroboros-mock builder.
	policyId := policyIdHash // Use the same hash as the filter
	output, err := mockledger.NewTransactionOutputBuilder().
		WithLovelace(1000000).
		WithAssets(mockledger.Asset{
			PolicyId:  policyId[:],
			AssetName: []byte("asset1"),
			Amount:    1,
		}).
		Build()
	require.NoError(t, err, "building mock transaction output")

	evt := event.Event{
		Payload: event.TransactionEvent{
			Outputs:        []ledger.TransactionOutput{output},
			ResolvedInputs: []ledger.TransactionOutput{output},
		},
	}

	// Start the filter
	err = cs.Start()
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

	// Build a transaction output whose asset fingerprint matches the filter
	// using the shared ouroboros-mock builder.
	policyId := common.Blake2b224Hash([]byte("policy1"))
	output, err := mockledger.NewTransactionOutputBuilder().
		WithLovelace(1000000).
		WithAssets(mockledger.Asset{
			PolicyId:  policyId[:],
			AssetName: []byte("asset1"),
			Amount:    1,
		}).
		Build()
	require.NoError(t, err, "building mock transaction output")

	evt := event.Event{
		Payload: event.TransactionEvent{
			Outputs:        []ledger.TransactionOutput{output},
			ResolvedInputs: []ledger.TransactionOutput{output},
		},
	}

	// Start the filter
	err = cs.Start()
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

func TestFilterByDRepIdTransactionEvent(t *testing.T) {
	// 28 bytes = 56 hex chars for Blake2b224
	drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
	drepHashBytes, _ := hex.DecodeString(drepHex)
	var drepCredHash common.CredentialHash
	copy(drepCredHash[:], drepHashBytes)

	t.Run("matches DRep registration certificate in tx", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		regCert := &common.RegistrationDrepCertificate{
			DrepCredential: common.Credential{
				CredType:   0,
				Credential: drepCredHash,
			},
		}

		output, err := mockledger.NewTransactionOutputBuilder().
			WithLovelace(1000000).
			Build()
		require.NoError(t, err, "building mock transaction output")

		evt := event.Event{
			Payload: event.TransactionEvent{
				Outputs:      []ledger.TransactionOutput{output},
				Certificates: []ledger.Certificate{regCert},
			},
		}

		err = cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("matches vote delegation certificate in tx", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		voteDelegCert := &common.VoteDelegationCertificate{
			StakeCredential: common.Credential{},
			Drep: common.Drep{
				Type:       common.DrepTypeAddrKeyHash,
				Credential: drepHashBytes,
			},
		}

		output, err := mockledger.NewTransactionOutputBuilder().
			WithLovelace(1000000).
			Build()
		require.NoError(t, err, "building mock transaction output")

		evt := event.Event{
			Payload: event.TransactionEvent{
				Outputs:      []ledger.TransactionOutput{output},
				Certificates: []ledger.Certificate{voteDelegCert},
			},
		}

		err = cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run(
		"does not match when no DRep certificates present",
		func(t *testing.T) {
			cs := New(WithDRepIds([]string{drepHex}))

			output, err := mockledger.NewTransactionOutputBuilder().
				WithLovelace(1000000).
				Build()
			require.NoError(t, err, "building mock transaction output")

			evt := event.Event{
				Payload: event.TransactionEvent{
					Outputs: []ledger.TransactionOutput{output},
					// No certificates
				},
			}

			err = cs.Start()
			assert.NoError(t, err)
			defer cs.Stop()

			cs.InputChan() <- evt

			select {
			case <-cs.OutputChan():
				t.Error(
					"Expected event to be filtered out but it passed through",
				)
			case <-time.After(100 * time.Millisecond):
				// Expected no event
			}
		},
	)
}

func TestFilterByPoolOrDRepIdTransactionEvent(t *testing.T) {
	poolHex := "abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd"
	drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
	drepHashBytes, _ := hex.DecodeString(drepHex)
	var drepCredHash common.CredentialHash
	copy(drepCredHash[:], drepHashBytes)

	cs := New(WithPoolIds([]string{poolHex}), WithDRepIds([]string{drepHex}))
	te := event.TransactionEvent{
		Certificates: []ledger.Certificate{
			&common.RegistrationDrepCertificate{
				DrepCredential: common.Credential{
					CredType:   0,
					Credential: drepCredHash,
				},
			},
		},
	}

	assert.True(t, cs.filterTransactionEvent(te))
}

func TestFilterByDRepIdGovernanceEvent(t *testing.T) {
	// 28 bytes = 56 hex chars for Blake2b224
	drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"

	t.Run("matches DRep registration certificate", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				DRepCertificates: []event.DRepCertificateData{
					{
						CertificateType: "Registration",
						DRepHash:        drepHex,
						DRepId:          "drep1...",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("matches vote delegation TO filtered DRep", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				VoteDelegationCertificates: []event.VoteDelegationCertificateData{
					{
						CertificateType: "VoteDelegation",
						DRepType:        "KeyHash",
						DRepHash:        drepHex,
						DRepId:          "drep1...",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("matches voting procedure BY filtered DRep", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{
						VoterType: "DRep",
						VoterHash: drepHex,
						Vote:      "Yes",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("does not match when DRep not in filter", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		otherDrepHex := "99999999999999999999999999999999999999999999999999999999"
		evt := event.Event{
			Payload: event.GovernanceEvent{
				DRepCertificates: []event.DRepCertificateData{
					{
						CertificateType: "Registration",
						DRepHash:        otherDrepHex,
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case <-cs.OutputChan():
			t.Error("Expected event to be filtered out but it passed through")
		case <-time.After(100 * time.Millisecond):
			// Expected no event
		}
	})

	t.Run("does not match Abstain delegation type", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				VoteDelegationCertificates: []event.VoteDelegationCertificateData{
					{
						CertificateType: "VoteDelegation",
						DRepType:        "Abstain",
						// No DRepHash for Abstain
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case <-cs.OutputChan():
			t.Error("Expected Abstain delegation to be filtered out")
		case <-time.After(100 * time.Millisecond):
			// Expected no event
		}
	})
}

func TestFilterByDRepIdDRepCertificateEvent(t *testing.T) {
	// 28 bytes = 56 hex chars for Blake2b224
	drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"

	t.Run("matches DRep certificate event", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		evt := event.Event{
			Payload: event.DRepCertificateEvent{
				Certificate: event.DRepCertificateData{
					CertificateType: event.DRepCertificateTypeRegistration,
					DRepHash:        drepHex,
					DRepId:          "drep1...",
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("does not match when DRep not in filter", func(t *testing.T) {
		cs := New(WithDRepIds([]string{drepHex}))

		evt := event.Event{
			Payload: event.DRepCertificateEvent{
				Certificate: event.DRepCertificateData{
					CertificateType: event.DRepCertificateTypeRegistration,
					DRepHash:        "deadbeef",
					DRepId:          "drep1notmatched",
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case <-cs.OutputChan():
			t.Error("Expected event to be filtered out but it passed through")
		case <-time.After(100 * time.Millisecond):
			// Expected no event
		}
	})
}

func TestWithDRepIds(t *testing.T) {
	t.Run("stores bech32 drep ID and computes hex", func(t *testing.T) {
		// Create a valid drep bech32 ID from known hex (28 bytes = 56 hex chars)
		drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
		hexBytes, _ := hex.DecodeString(drepHex)
		convData, _ := bech32.ConvertBits(hexBytes, 8, 5, true)
		drepBech32, _ := bech32.Encode("drep", convData)

		cs := New(WithDRepIds([]string{drepBech32}))

		assert.True(t, cs.filterSet.hasDRepFilter)
		assert.NotNil(t, cs.filterSet.dreps)
		// Should have the bech32 version stored
		_, hasBech32 := cs.filterSet.dreps.bech32DRepIds[drepBech32]
		assert.True(t, hasBech32, "should store bech32 drep ID")
		// Should have computed hex version
		_, hasHex := cs.filterSet.dreps.hexDRepIds[drepHex]
		assert.True(t, hasHex, "should compute and store hex drep ID")
	})

	t.Run(
		"stores hex drep ID and computes bech32 variants",
		func(t *testing.T) {
			drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
			hexBytes, _ := hex.DecodeString(drepHex)
			convData, _ := bech32.ConvertBits(hexBytes, 8, 5, true)
			expectedDrep, _ := bech32.Encode("drep", convData)
			expectedDrepScript, _ := bech32.Encode("drep_script", convData)

			cs := New(WithDRepIds([]string{drepHex}))

			assert.True(t, cs.filterSet.hasDRepFilter)
			// Should have the hex version stored
			_, hasHex := cs.filterSet.dreps.hexDRepIds[drepHex]
			assert.True(t, hasHex, "should store hex drep ID")
			// Should have computed both bech32 variants (drep and drep_script)
			_, hasDrep := cs.filterSet.dreps.bech32DRepIds[expectedDrep]
			assert.True(t, hasDrep, "should compute bech32 drep variant")
			_, hasDrepScript := cs.filterSet.dreps.bech32DRepIds[expectedDrepScript]
			assert.True(t, hasDrepScript, "should compute bech32 drep_script variant")
		},
	)

	t.Run("handles drep_script bech32 prefix", func(t *testing.T) {
		drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
		hexBytes, _ := hex.DecodeString(drepHex)
		convData, _ := bech32.ConvertBits(hexBytes, 8, 5, true)
		drepScriptBech32, _ := bech32.Encode("drep_script", convData)

		cs := New(WithDRepIds([]string{drepScriptBech32}))

		assert.True(t, cs.filterSet.hasDRepFilter)
		// Should have the bech32 version stored
		_, hasBech32 := cs.filterSet.dreps.bech32DRepIds[drepScriptBech32]
		assert.True(t, hasBech32, "should store drep_script bech32 ID")
		// Should have computed hex version
		_, hasHex := cs.filterSet.dreps.hexDRepIds[drepHex]
		assert.True(
			t,
			hasHex,
			"should compute and store hex drep ID from drep_script",
		)
	})

	t.Run(
		"handles CIP-0129 bech32 drep ID with header byte",
		func(t *testing.T) {
			// CIP-0129 bech32 dRep addresses include a 1-byte header before the 28-byte hash.
			// The header for a key hash dRep is 0x22.
			drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
			hexBytes, _ := hex.DecodeString(drepHex)
			// Build CIP-0129 payload: header (0x22) + 28-byte hash
			cip129Payload := append([]byte{0x22}, hexBytes...)
			convData, _ := bech32.ConvertBits(cip129Payload, 8, 5, true)
			cip129Bech32, _ := bech32.Encode("drep", convData)

			cs := New(WithDRepIds([]string{cip129Bech32}))

			assert.True(t, cs.filterSet.hasDRepFilter)
			// Must store the raw 28-byte hex (without the CIP-0129 header) so it
			// matches voter.Hash and DRepHash event fields.
			_, hasHex := cs.filterSet.dreps.hexDRepIds[drepHex]
			assert.True(
				t,
				hasHex,
				"should strip CIP-0129 header and store 28-byte hex",
			)
		},
	)

	t.Run(
		"CIP-0129 bech32 drep matches governance voting procedure",
		func(t *testing.T) {
			drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
			hexBytes, _ := hex.DecodeString(drepHex)
			// Build CIP-0129 bech32 (header 0x22 + 28-byte hash)
			cip129Payload := append([]byte{0x22}, hexBytes...)
			convData, _ := bech32.ConvertBits(cip129Payload, 8, 5, true)
			cip129Bech32, _ := bech32.Encode("drep", convData)

			cs := New(WithDRepIds([]string{cip129Bech32}))

			// Simulate a governance event where VoterHash is the raw 28-byte hex
			// (as produced by hex.EncodeToString(voter.Hash[:]) in the event layer)
			ge := event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{
						VoterType: "DRep",
						VoterHash: drepHex,
					},
				},
			}
			assert.True(
				t,
				cs.filterGovernanceEvent(ge),
				"CIP-0129 bech32 input should match governance voting procedure",
			)
		},
	)

	t.Run(
		"real-world CIP-0129 drep address matches governance vote",
		func(t *testing.T) {
			// drep1yg8vjs7ute7z7vyd8yez5tgjey6043djjfh8d3n7sjev35g064xxc is a real
			// CIP-0129 bech32 dRep address; its raw 28-byte hash is the hex below.
			cip129Bech32 := "drep1yg8vjs7ute7z7vyd8yez5tgjey6043djjfh8d3n7sjev35g064xxc"
			expectedHex := "0ec943dc5e7c2f308d39322a2d12c934fac5b2926e76c67e84b2c8d1"

			cs := New(WithDRepIds([]string{cip129Bech32}))

			assert.True(t, cs.filterSet.hasDRepFilter)
			_, hasHex := cs.filterSet.dreps.hexDRepIds[expectedHex]
			assert.True(
				t,
				hasHex,
				"should decode CIP-0129 address to 28-byte hex",
			)

			ge := event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{VoterType: "DRep", VoterHash: expectedHex},
				},
			}
			assert.True(
				t,
				cs.filterGovernanceEvent(ge),
				"real-world CIP-0129 drep address should match its governance vote",
			)
		},
	)

	t.Run("skips invalid input without crashing", func(t *testing.T) {
		cs := New(WithDRepIds([]string{"invalid_bech32", "", "xyz"}))

		// Should not crash, filter should not be enabled with only invalid IDs
		assert.False(t, cs.filterSet.hasDRepFilter)
	})
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

func TestFilterByPoolIdGovernanceEvent(t *testing.T) {
	// Pool key hash is 28 bytes = 56 hex chars (Blake2b-224)
	poolHex := "abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd"

	t.Run("matches voting procedure BY filtered SPO", func(t *testing.T) {
		cs := New(WithPoolIds([]string{poolHex}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{
						VoterType: "SPO",
						VoterHash: poolHex,
						Vote:      "Yes",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run(
		"matches vote delegation certificate with PoolKeyHash",
		func(t *testing.T) {
			cs := New(WithPoolIds([]string{poolHex}))

			evt := event.Event{
				Payload: event.GovernanceEvent{
					VoteDelegationCertificates: []event.VoteDelegationCertificateData{
						{
							CertificateType: "StakeVoteDelegation",
							PoolKeyHash:     poolHex,
						},
					},
				},
			}

			err := cs.Start()
			assert.NoError(t, err)
			defer cs.Stop()

			cs.InputChan() <- evt

			select {
			case filteredEvt := <-cs.OutputChan():
				assert.Equal(t, evt, filteredEvt)
			case <-time.After(1 * time.Second):
				t.Error("Expected event to pass filter but it didn't")
			}
		},
	)

	t.Run("does not match when pool not in filter", func(t *testing.T) {
		cs := New(WithPoolIds([]string{poolHex}))

		otherPoolHex := "99999999999999999999999999999999999999999999999999999999"
		evt := event.Event{
			Payload: event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{
						VoterType: "SPO",
						VoterHash: otherPoolHex,
						Vote:      "Yes",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case <-cs.OutputChan():
			t.Error("Expected event to be filtered out but it passed through")
		case <-time.After(100 * time.Millisecond):
			// Expected no event
		}
	})
}

func TestFilterByPoolOrDRepIdGovernanceEvent(t *testing.T) {
	poolHex := "abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd"
	drepHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"
	otherHex := "99999999999999999999999999999999999999999999999999999999"
	cs := New(WithPoolIds([]string{poolHex}), WithDRepIds([]string{drepHex}))

	tests := []struct {
		name        string
		governance  event.GovernanceEvent
		shouldMatch bool
	}{
		{
			name: "matches followed DRep vote without pool activity",
			governance: event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{VoterType: "DRep", VoterHash: drepHex, Vote: "Yes"},
				},
			},
			shouldMatch: true,
		},
		{
			name: "matches followed pool vote without DRep activity",
			governance: event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{VoterType: "SPO", VoterHash: poolHex, Vote: "Yes"},
				},
			},
			shouldMatch: true,
		},
		{
			name: "rejects unrelated governance activity",
			governance: event.GovernanceEvent{
				VotingProcedures: []event.VotingProcedureData{
					{VoterType: "DRep", VoterHash: otherHex, Vote: "Yes"},
				},
			},
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.shouldMatch, cs.filterGovernanceEvent(tt.governance))
		})
	}
}

func TestFilterByAddressGovernanceEvent(t *testing.T) {
	// Use a stake/reward address format for proposal reward accounts
	stakeAddr := "stake1uyehkck0lajq8gr28t9uxnuvgcqrc6070x3k9r8048z8y5gh6ffgw"
	// 28 bytes = 56 hex chars for stake credential hash
	stakeCredHex := "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234"

	t.Run("matches proposal reward account", func(t *testing.T) {
		cs := New(WithAddresses([]string{stakeAddr}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				ProposalProcedures: []event.ProposalProcedureData{
					{
						Index:         0,
						Deposit:       100000000,
						RewardAccount: stakeAddr,
						ActionType:    "Info",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("matches treasury withdrawal address", func(t *testing.T) {
		// Use a payment address for treasury withdrawal
		paymentAddr := "addr1qx2fxv2umyhttkxyxp8x0dlpdt3k6cwng5pxj3jhsydzer3jcu5d8ps7zex2k2xt3uqxgjqnnj83ws8lhrn648jjxtwq2ytjqp"
		cs := New(WithAddresses([]string{paymentAddr}))

		evt := event.Event{
			Payload: event.GovernanceEvent{
				ProposalProcedures: []event.ProposalProcedureData{
					{
						Index:         0,
						Deposit:       100000000000,
						RewardAccount: "stake1...", // Different from filter
						ActionType:    "TreasuryWithdrawal",
						ActionData: event.GovActionData{
							TreasuryWithdrawal: &event.TreasuryWithdrawalActionData{
								Withdrawals: []event.TreasuryWithdrawalItem{
									{
										Address: paymentAddr,
										Amount:  50000000000,
									},
								},
							},
						},
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("matches vote delegation stake credential", func(t *testing.T) {
		// Create a stake address that decodes to a credential hash
		realStakeAddr := "stake1uyehkck0lajq8gr28t9uxnuvgcqrc6070x3k9r8048z8y5gh6ffgw"
		cs := New(WithAddresses([]string{realStakeAddr}))

		// Decode the stake address to get the actual credential hash
		_, data, err := bech32.DecodeNoLimit(realStakeAddr)
		assert.NoError(t, err)
		converted, err := bech32.ConvertBits(data, 5, 8, false)
		assert.NoError(t, err)
		// Skip the first byte (header) to get the credential hash
		credHash := hex.EncodeToString(converted[1:])

		evt := event.Event{
			Payload: event.GovernanceEvent{
				VoteDelegationCertificates: []event.VoteDelegationCertificateData{
					{
						CertificateType: "VoteDelegation",
						StakeCredential: credHash,
						DRepType:        "KeyHash",
						DRepHash:        "someDRepHash",
					},
				},
			},
		}

		err = cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case filteredEvt := <-cs.OutputChan():
			assert.Equal(t, evt, filteredEvt)
		case <-time.After(1 * time.Second):
			t.Error("Expected event to pass filter but it didn't")
		}
	})

	t.Run("does not match when address not in filter", func(t *testing.T) {
		cs := New(WithAddresses([]string{stakeAddr}))

		otherStakeAddr := "stake1uxxx..."
		evt := event.Event{
			Payload: event.GovernanceEvent{
				ProposalProcedures: []event.ProposalProcedureData{
					{
						Index:         0,
						RewardAccount: otherStakeAddr,
						ActionType:    "Info",
					},
				},
			},
		}

		err := cs.Start()
		assert.NoError(t, err)
		defer cs.Stop()

		cs.InputChan() <- evt

		select {
		case <-cs.OutputChan():
			t.Error("Expected event to be filtered out but it passed through")
		case <-time.After(100 * time.Millisecond):
			// Expected no event
		}
	})

	t.Run(
		"does not match governance event with no address data",
		func(t *testing.T) {
			cs := New(WithAddresses([]string{stakeAddr}))

			evt := event.Event{
				Payload: event.GovernanceEvent{
					// Only voting procedures, no addresses
					VotingProcedures: []event.VotingProcedureData{
						{
							VoterType: "DRep",
							VoterHash: stakeCredHex,
							Vote:      "Yes",
						},
					},
				},
			}

			err := cs.Start()
			assert.NoError(t, err)
			defer cs.Stop()

			cs.InputChan() <- evt

			select {
			case <-cs.OutputChan():
				t.Error(
					"Expected event to be filtered out but it passed through",
				)
			case <-time.After(100 * time.Millisecond):
				// Expected no event
			}
		},
	)
}
