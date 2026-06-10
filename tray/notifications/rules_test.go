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

package notifications

import (
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// txEvent builds a JSON-shaped transaction event as it arrives over the
// wire (Payload/Context decoded into map[string]any with float64
// numbers).
func txEvent(hash string, magic float64) event.Event {
	return event.Event{
		Type:    EventTypeTransaction,
		Payload: map[string]any{"transactionHash": hash},
		Context: map[string]any{"networkMagic": magic},
	}
}

func blockEvent(hash string) event.Event {
	return event.Event{
		Type:    EventTypeBlock,
		Payload: map[string]any{"blockHash": hash},
	}
}

func govEvent() event.Event {
	return event.Event{
		Type:    EventTypeGovernance,
		Payload: map[string]any{},
	}
}

func TestMatchExprEvaluate(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		event event.Event
		want  bool
	}{
		{
			name:  "empty expr always matches",
			expr:  "",
			event: blockEvent("abc"),
			want:  true,
		},
		{
			name:  "payload dotted path equals",
			expr:  "payload.blockHash=abc",
			event: blockEvent("abc"),
			want:  true,
		},
		{
			name:  "payload dotted path mismatch value",
			expr:  "payload.blockHash=xyz",
			event: blockEvent("abc"),
			want:  false,
		},
		{
			name:  "context numeric path matches",
			expr:  "context.networkMagic=2",
			event: txEvent("hh", 2),
			want:  true,
		},
		{
			name:  "context numeric path mismatch",
			expr:  "context.networkMagic=1",
			event: txEvent("hh", 2),
			want:  false,
		},
		{
			name:  "missing path does not match",
			expr:  "payload.nope=x",
			event: blockEvent("abc"),
			want:  false,
		},
		{
			name:  "malformed expr (no equals) never matches",
			expr:  "payload.blockHash",
			event: blockEvent("abc"),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalMatchExpr(tt.expr, tt.event))
		})
	}
}

func TestRuleMatches(t *testing.T) {
	r := Rule{
		Enabled:   true,
		EventType: EventTypeBlock,
		MatchExpr: "payload.blockHash=abc",
	}
	assert.True(t, r.Matches(blockEvent("abc")))
	// wrong type
	assert.False(t, r.Matches(txEvent("abc", 1)))
	// disabled never matches
	r.Enabled = false
	assert.False(t, r.Matches(blockEvent("abc")))
}

func TestRulesFromPlan_MonitorEverything(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{MonitorEverything: true},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefBlocksMinted:     true,
			setup.NotifyPrefIncomingTx:       true,
			setup.NotifyPrefVotesCast:        false,
			setup.NotifyPrefConnectionIssues: true,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: an enabled block rule exists and matches a block event.
	require.True(t, anyRuleMatches(rules, blockEvent("h")),
		"blocks-minted rule should match a block event")
	// Positive: incoming-tx rule matches a transaction event.
	require.True(t, anyRuleMatches(rules, txEvent("h", 1)),
		"incoming-tx rule should match a transaction event")
	// Negative: votes-cast disabled, so no rule matches governance.
	require.False(t, anyRuleMatches(rules, govEvent()),
		"votes-cast disabled: governance must not match")
	// Connection rule present and enabled.
	require.True(t, anyRuleMatches(rules, ConnectionEvent("x")),
		"connection rule should match a connection event")
}

func TestRulesFromPlan_WatchWallet(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"addr_test1", "stake_test1"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:     true,
			setup.NotifyPrefOutgoingTx:     true,
			setup.NotifyPrefTokenTransfers: false,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: transaction matches.
	require.True(t, anyRuleMatches(rules, txEvent("h", 1)),
		"wallet tx rule should match a transaction event")
	// Negative: a block event must NOT match a wallet plan.
	require.False(t, anyRuleMatches(rules, blockEvent("h")),
		"wallet plan must not match block events")
	// One rule per param value: at least two enabled tx rules.
	enabledTx := 0
	for _, r := range rules {
		if r.Enabled && r.EventType == EventTypeTransaction {
			enabledTx++
		}
	}
	require.GreaterOrEqual(t, enabledTx, 2,
		"expected one transaction rule per param value")
}

func TestRulesFromPlan_TrackDRep(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			DReps: []string{"drep1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefGovProposals: true,
			setup.NotifyPrefVotesCast:    true,
			setup.NotifyPrefRegChanges:   false,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: governance matches.
	require.True(t, anyRuleMatches(rules, govEvent()),
		"drep gov rule should match a governance event")
	// Negative: a transaction must NOT match a DRep plan.
	require.False(t, anyRuleMatches(rules, txEvent("h", 1)),
		"drep plan must not match transaction events")
}

func TestRulesFromPlan_MonitorPool(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Pools: []string{"pool1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefBlocksMinted: true,
			setup.NotifyPrefPoolParams:   true,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: block matches.
	require.True(t, anyRuleMatches(rules, blockEvent("h")),
		"pool block rule should match a block event")
	// Negative: governance must NOT match a pool plan.
	require.False(t, anyRuleMatches(rules, govEvent()),
		"pool plan must not match governance events")
}

func TestRulesFromPlan_AllDisabled(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{MonitorEverything: true},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefBlocksMinted:     false,
			setup.NotifyPrefIncomingTx:       false,
			setup.NotifyPrefVotesCast:        false,
			setup.NotifyPrefConnectionIssues: false,
		},
	}
	rules := RulesFromPlan(plan)
	require.False(t, anyRuleMatches(rules, blockEvent("h")))
	require.False(t, anyRuleMatches(rules, txEvent("h", 1)))
	require.False(t, anyRuleMatches(rules, govEvent()))
	require.False(t, anyRuleMatches(rules, ConnectionEvent("x")))
}

// TestRulesFromPlan_MonitorEverythingORsRelevantPrefs guards the
// review-feedback regression: with MonitorEverything on, a coarse rule
// per event family must fire if ANY pref relevant to that family is
// enabled — not just the first one we happen to key off. Toggling only
// OutgoingTx (without IncomingTx) must still produce a tx alert.
func TestRulesFromPlan_MonitorEverythingORsRelevantPrefs(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{MonitorEverything: true},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefOutgoingTx:   true, // tx family
			setup.NotifyPrefRegChanges:   true, // gov family
			setup.NotifyPrefPoolParams:   true, // block family
			setup.NotifyPrefIncomingTx:   false,
			setup.NotifyPrefVotesCast:    false,
			setup.NotifyPrefBlocksMinted: false,
		},
	}
	rules := RulesFromPlan(plan)
	assert.True(t, anyRuleMatches(rules, txEvent("h", 1)),
		"OutgoingTx alone must enable the everything-tx rule")
	assert.True(t, anyRuleMatches(rules, govEvent()),
		"RegChanges alone must enable the everything-gov rule")
	assert.True(t, anyRuleMatches(rules, blockEvent("h")),
		"PoolParams alone must enable the everything-block rule")
}

// TestRulesFromPlan_EmptyTargetListsEmitNoRules guards the
// review-feedback regression: per-target sections with empty lists must
// not produce catch-all rules. An unconfigured wallet/DRep/pool section
// means "no rule for this kind", not "match every event of this kind".
func TestRulesFromPlan_EmptyTargetListsEmitNoRules(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			// All three lists empty; MonitorEverything off.
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:       true,
			setup.NotifyPrefVotesCast:        true,
			setup.NotifyPrefBlocksMinted:     true,
			setup.NotifyPrefConnectionIssues: true,
		},
	}
	rules := RulesFromPlan(plan)
	assert.False(t, anyRuleMatches(rules, txEvent("h", 1)),
		"no wallets configured → no tx rule")
	assert.False(t, anyRuleMatches(rules, govEvent()),
		"no DReps configured → no gov rule")
	assert.False(t, anyRuleMatches(rules, blockEvent("h")),
		"no pools configured → no block rule")
	// The always-on connection rule is independent of the per-target
	// lists and must still be present.
	assert.True(t, anyRuleMatches(rules, ConnectionEvent("x")),
		"connection rule is independent of per-target lists")
}

// TestRulesFromPlan_ParamPreserved verifies the rule-set carries each
// template parameter through to Rule.Param so a later address-aware
// filter can read the address/DRep/pool ID off the rule without
// re-parsing the setup plan. Covers each kind independently and a
// combined multi-target plan.
func TestRulesFromPlan_ParamPreserved(t *testing.T) {
	cases := []struct {
		name       string
		filter     setup.FilterConfig
		pref       string
		wantParams map[string]bool
	}{
		{
			name: "wallet",
			filter: setup.FilterConfig{
				Wallets: []string{"addr_test1", "stake_test1"},
			},
			pref: setup.NotifyPrefIncomingTx,
			wantParams: map[string]bool{
				"addr_test1": true, "stake_test1": true,
			},
		},
		{
			name: "drep",
			filter: setup.FilterConfig{
				DReps: []string{"drep1abc", "drep1def"},
			},
			pref: setup.NotifyPrefVotesCast,
			wantParams: map[string]bool{
				"drep1abc": true, "drep1def": true,
			},
		},
		{
			name: "pool",
			filter: setup.FilterConfig{
				Pools: []string{"pool1abc", "pool1def"},
			},
			pref: setup.NotifyPrefBlocksMinted,
			wantParams: map[string]bool{
				"pool1abc": true, "pool1def": true,
			},
		},
		{
			// Combined: wallet AND drep AND pool simultaneously.
			// Each list contributes its params; the engine sees all
			// of them on the emitted rules.
			name: "combined wallet+drep+pool",
			filter: setup.FilterConfig{
				Wallets: []string{"addr1xyz"},
				DReps:   []string{"drep1abc"},
				Pools:   []string{"pool1abc"},
			},
			pref: setup.NotifyPrefIncomingTx,
			wantParams: map[string]bool{
				"addr1xyz": true,
				"drep1abc": true,
				"pool1abc": true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := RulesFromPlan(setup.SetupPlan{
				Filter: tc.filter,
				Notify: setup.NotificationPrefs{tc.pref: true},
			})
			got := map[string]bool{}
			for _, r := range rules {
				if r.Param != "" {
					got[r.Param] = true
				}
			}
			assert.Equal(t, tc.wantParams, got,
				"every supplied param must appear on at least one rule")
		})
	}
}

func TestRulesFromPlan_RuleIDsUnique(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"addr1", "addr2"},
			DReps:   []string{"drep1abc"},
			Pools:   []string{"pool1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:   true,
			setup.NotifyPrefOutgoingTx:   true,
			setup.NotifyPrefVotesCast:    true,
			setup.NotifyPrefBlocksMinted: true,
		},
	}
	rules := RulesFromPlan(plan)
	seen := map[string]bool{}
	for _, r := range rules {
		require.NotEmpty(t, r.ID)
		require.False(t, seen[r.ID], "duplicate rule ID: %s", r.ID)
		seen[r.ID] = true
	}
}

// anyRuleMatches reports whether any rule in rules matches evt. Lives in
// a _test.go file so the linter (tests:false) does not flag it as unused
// in the non-test build.
func anyRuleMatches(rules []Rule, evt event.Event) bool {
	for _, r := range rules {
		if r.Matches(evt) {
			return true
		}
	}
	return false
}
