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

package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClonePlanIsDeep(t *testing.T) {
	orig := SetupPlan{
		Filter: FilterConfig{
			Wallets:  []string{"addr1a"},
			DReps:    []string{"drep1a"},
			Pools:    []string{"pool1a"},
			Assets:   []string{"asset1a"},
			Policies: []string{"pol1a"},
		},
		Notify: NotificationPrefs{NotifyPrefIncomingTx: true},
		Output: OutputConfig{Config: map[string]string{"k": "v"}},
	}
	clone := ClonePlan(orig)
	clone.Filter.Wallets = append(clone.Filter.Wallets, "addr1b")
	clone.Filter.DReps[0] = "drep1z"
	clone.Filter.Assets[0] = "asset1z"
	clone.Notify[NotifyPrefIncomingTx] = false
	clone.Notify[NotifyPrefOutgoingTx] = true
	clone.Output.Config["k"] = "other"

	assert.Equal(t, []string{"addr1a"}, orig.Filter.Wallets)
	assert.Equal(t, "drep1a", orig.Filter.DReps[0])
	assert.Equal(t, "asset1a", orig.Filter.Assets[0])
	assert.True(t, orig.Notify[NotifyPrefIncomingTx])
	_, hasOutgoing := orig.Notify[NotifyPrefOutgoingTx]
	assert.False(t, hasOutgoing)
	assert.Equal(t, "v", orig.Output.Config["k"])
}

func TestClonePlanNilMapsStaySafe(t *testing.T) {
	// Nil maps/slices must not panic and must not allocate empty
	// non-nil maps that would surprise callers checking for nil.
	orig := SetupPlan{}
	clone := ClonePlan(orig)
	assert.Nil(t, clone.Notify)
	assert.Nil(t, clone.Output.Config)
}

// TestAllNotifyPrefsExhaustive asserts AllNotifyPrefs lists every
// NotifyPref* constant declared in plan.go exactly once. Locks in
// the single-source-of-truth invariant so adding a new pref forces
// updating AllNotifyPrefs (otherwise the rules editor silently
// hides the toggle).
func TestAllNotifyPrefsExhaustive(t *testing.T) {
	want := []string{
		NotifyPrefBlocksMinted,
		NotifyPrefIncomingTx,
		NotifyPrefOutgoingTx,
		NotifyPrefTokenTransfers,
		NotifyPrefGovProposals,
		NotifyPrefVotesCast,
		NotifyPrefRegChanges,
		NotifyPrefPoolParams,
		NotifyPrefAssetActivity,
		NotifyPrefPolicyActivity,
		NotifyPrefConnectionIssues,
	}
	got := AllNotifyPrefs()
	require.Len(t, got, len(want))
	seen := make(map[string]struct{}, len(got))
	for _, p := range got {
		_, dup := seen[p]
		assert.False(t, dup, "pref %q listed twice", p)
		seen[p] = struct{}{}
	}
	for _, p := range want {
		_, ok := seen[p]
		assert.Truef(t, ok, "AllNotifyPrefs missing %q", p)
	}
	// Accessor returns a fresh slice — mutating it must not affect
	// the canonical backing array.
	require.NotEmpty(t, got)
	got[0] = "X"
	again := AllNotifyPrefs()
	require.NotEmpty(t, again)
	assert.NotEqual(t, "X", again[0])
}
