// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package cardano

import (
	"strings"
	"sync"
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	mockledger "github.com/blinklabs-io/ouroboros-mock/ledger"
	"github.com/stretchr/testify/require"
)

func TestCompiledMatcherEventFamilies(t *testing.T) {
	for _, evt := range []event.Event{
		{Type: event.TypeBlock, Payload: event.BlockEvent{}},
		{Type: event.TypeTransaction, Payload: event.TransactionEvent{}},
		{Type: event.TypeGovernance, Payload: event.GovernanceEvent{}},
		{Type: event.TypeDRepRegistration, Payload: event.DRepCertificateEvent{}},
		{Type: event.TypeDRepUpdate, Payload: event.DRepCertificateEvent{}},
		{Type: event.TypeDRepRetirement, Payload: event.DRepCertificateEvent{}},
		{Type: event.TypeRollback, Payload: event.RollbackEvent{}},
	} {
		t.Run(evt.Type, func(t *testing.T) {
			matcher, err := Compile(evt.Type, Criteria{})
			require.NoError(t, err)
			require.True(t, matcher.Match(evt))
			require.False(t, matcher.Match(event.Event{Type: evt.Type}))
			require.False(t, matcher.Match(event.Event{
				Type: "custom", Payload: evt.Payload,
			}))
			require.False(t, matcher.Match(event.Event{
				Type: evt.Type, Payload: map[string]any{},
			}))
		})
	}
	for _, name := range []string{"", "block", "input.Block", "custom"} {
		_, err := Compile(name, Criteria{})
		require.Error(t, err)
	}
	legacy := New(WithPoolIds([]string{strings.Repeat("ab", 28)}))
	require.True(t, legacy.filterEvent(event.Event{
		Type: event.TypeRollback, Payload: event.RollbackEvent{},
	}))
	matcher, err := Compile(event.TypeBlock, Criteria{})
	require.NoError(t, err)
	require.False(t, matcher.Match(event.Event{
		Type: event.TypeRollback, Payload: event.RollbackEvent{},
	}))
}

func TestCompiledMatcherCriterionFamilies(t *testing.T) {
	const address = "stake1uyehkck0lajq8gr28t9uxnuvgcqrc6070x3k9r8048z8y5gh6ffgw"
	criteria := []struct {
		name    string
		value   Criteria
		allowed []string
	}{
		{"address", Criteria{Addresses: []string{address}}, []string{event.TypeTransaction, event.TypeGovernance}},
		{"policy", Criteria{Policies: []string{strings.Repeat("ab", 28)}}, []string{event.TypeTransaction}},
		{"asset", Criteria{AssetFingerprints: []string{"asset1e58wmplshqdkkq97tz02chq980456wgt35tfjr"}}, []string{event.TypeTransaction}},
		{"pool", Criteria{PoolIDs: []string{strings.Repeat("cd", 28)}}, []string{event.TypeBlock, event.TypeTransaction, event.TypeGovernance}},
		{"drep", Criteria{DRepIDs: []string{strings.Repeat("ef", 28)}}, []string{event.TypeTransaction, event.TypeGovernance, event.TypeDRepRegistration, event.TypeDRepUpdate, event.TypeDRepRetirement}},
	}
	for _, c := range criteria {
		for _, family := range []string{
			event.TypeBlock, event.TypeTransaction, event.TypeGovernance,
			event.TypeDRepRegistration, event.TypeDRepUpdate,
			event.TypeDRepRetirement, event.TypeRollback,
		} {
			t.Run(c.name+"/"+family, func(t *testing.T) {
				allowed := false
				for _, name := range c.allowed {
					allowed = allowed || name == family
				}
				_, err := Compile(family, c.value)
				if allowed {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "incompatible")
				}
			})
		}
	}
}

func TestCompiledMatcherRejectsMalformedCriteria(t *testing.T) {
	for _, c := range []Criteria{
		{Addresses: []string{""}},
		{Addresses: []string{"secret-sentinel-not-an-address"}},
		{Policies: []string{"ab"}},
		{Policies: []string{strings.Repeat("AB", 28)}},
		{PoolIDs: []string{"pool1invalid"}},
		{DRepIDs: []string{"drep1invalid"}},
		{AssetFingerprints: []string{"asset1invalid"}},
		{DRepIDs: []string{""}},
	} {
		_, err := Compile(event.TypeTransaction, c)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret-sentinel")
	}
}

func TestCompiledMatcherPoolOrDRepAndAddress(t *testing.T) {
	const address = "stake1uyehkck0lajq8gr28t9uxnuvgcqrc6070x3k9r8048z8y5gh6ffgw"
	pool, drep := strings.Repeat("ab", 28), strings.Repeat("cd", 28)
	matcher, err := Compile(event.TypeGovernance, Criteria{
		Addresses: []string{address}, PoolIDs: []string{pool}, DRepIDs: []string{drep},
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, voterType, voterHash, reward string
		want                               bool
	}{
		{"pool", "SPO", pool, address, true},
		{"drep", "DRep", drep, address, true},
		{"unrelated identity", "DRep", strings.Repeat("ef", 28), address, false},
		{"address still required", "DRep", drep, "other", false},
		{"matching hash wrong role", "SPO", drep, address, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, matcher.Match(event.Event{
				Type: event.TypeGovernance,
				Payload: event.GovernanceEvent{
					ProposalProcedures: []event.ProposalProcedureData{{RewardAccount: tc.reward}},
					VotingProcedures:   []event.VotingProcedureData{{VoterType: tc.voterType, VoterHash: tc.voterHash}},
				},
			}))
		})
	}
}

func TestCompiledMatcherTransactionCriteria(t *testing.T) {
	const address = "addr_test1qz2fxv2umyhttkxyxp8x0dlpdt3k6cwng5pxj3jhsydzer3jcu5d8ps7zex2k2xt3uqxgjqnnj83ws8lhrn648jjxtwq2ytjqp"
	policy := common.Blake2b224Hash([]byte("policy1"))
	output, err := mockledger.NewTransactionOutputBuilder().
		WithAddress(address).
		WithAssets(mockledger.Asset{PolicyId: policy[:], AssetName: []byte("asset1"), Amount: 1}).Build()
	require.NoError(t, err)
	matcher, err := Compile(event.TypeTransaction, Criteria{
		Addresses: []string{address}, Policies: []string{policy.String()},
		AssetFingerprints: []string{"asset1e58wmplshqdkkq97tz02chq980456wgt35tfjr"},
	})
	require.NoError(t, err)
	require.True(t, matcher.Match(event.Event{
		Type:    event.TypeTransaction,
		Payload: event.TransactionEvent{ResolvedInputs: []ledger.TransactionOutput{output}},
	}))
	withoutAsset, err := mockledger.NewTransactionOutputBuilder().WithAddress(address).Build()
	require.NoError(t, err)
	require.False(t, matcher.Match(event.Event{
		Type:    event.TypeTransaction,
		Payload: event.TransactionEvent{Outputs: []ledger.TransactionOutput{withoutAsset}},
	}))
}

func TestCompiledMatcherOwnsCriteria(t *testing.T) {
	pool := strings.Repeat("ab", 28)
	criteria := Criteria{PoolIDs: []string{pool}}
	matcher, err := Compile(event.TypeBlock, criteria)
	require.NoError(t, err)
	criteria.PoolIDs[0] = strings.Repeat("cd", 28)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 100 {
				if !matcher.Match(event.Event{Type: event.TypeBlock, Payload: event.BlockEvent{IssuerVkey: pool}}) {
					t.Error("compiled pool lost after caller mutated criteria")
				}
				if matcher.Match(event.Event{Type: event.TypeBlock, Payload: event.BlockEvent{IssuerVkey: criteria.PoolIDs[0]}}) {
					t.Error("compiled pool changed after caller mutated criteria")
				}
			}
		})
	}
	workers.Wait()
}
