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

// txEventTo builds a transaction event with the given outputs (only the
// recipient side resolved) so wallet-incoming matchers have something
// to walk.
func txEventTo(outputAddr string) event.Event {
	return event.Event{
		Type: EventTypeTransaction,
		Payload: map[string]any{
			"outputs": []any{
				map[string]any{
					"address": outputAddr,
					"amount":  uint64(1_000_000),
				},
			},
		},
	}
}

// txEventFrom builds a transaction event with a resolved input from
// the given address — i.e. a transaction spending that wallet's UTxO.
func txEventFrom(inputAddr string) event.Event {
	return event.Event{
		Type: EventTypeTransaction,
		Payload: map[string]any{
			"resolvedInputs": []any{
				map[string]any{"address": inputAddr},
			},
			"outputs": []any{
				map[string]any{
					"address": "addr1qsomeoneelse",
					"amount":  uint64(1_000_000),
				},
			},
		},
	}
}

func blockEvent(hash string) event.Event {
	return event.Event{
		Type:    EventTypeBlock,
		Payload: map[string]any{"blockHash": hash},
	}
}

func poolBlockEvent(hash, pool string) event.Event {
	return event.Event{
		Type: EventTypeBlock,
		Payload: map[string]any{
			"blockHash":  hash,
			"issuerVkey": pool,
		},
	}
}

func govEvent() event.Event {
	return govEventForDRep("drep1abc")
}

func govEventForDRep(drep string) event.Event {
	return event.Event{
		Type: EventTypeGovernance,
		Payload: map[string]any{
			"votingProcedures": []any{
				map[string]any{
					"voterId":        drep,
					"vote":           "yes",
					"govActionIndex": float64(1),
				},
			},
		},
	}
}

func govProposal() event.Event {
	return event.Event{
		Type: EventTypeGovernance,
		Payload: map[string]any{
			"proposalProcedures": []any{
				map[string]any{"deposit": float64(1)},
			},
		},
	}
}

func govDRepCert(drep string) event.Event {
	return event.Event{
		Type: EventTypeGovernance,
		Payload: map[string]any{
			"drepCertificates": []any{
				map[string]any{"drepId": drep},
			},
		},
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
	require.True(t, anyRuleMatches(rules, ConnectionEvent("Adder Connection", "x")),
		"connection rule should match a connection event")
}

func TestRulesFromPlan_WatchWallet(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"addr_test1"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:     true,
			setup.NotifyPrefOutgoingTx:     true,
			setup.NotifyPrefTokenTransfers: false,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: incoming tx (wallet in outputs) matches.
	require.True(t, anyRuleMatches(rules, txEventTo("addr_test1")),
		"wallet rule should match a transaction sending to the wallet")
	// Positive: outgoing tx (wallet in resolvedInputs) matches.
	require.True(t, anyRuleMatches(rules, txEventFrom("addr_test1")),
		"wallet rule should match a transaction spending from the wallet")
	// Negative: a transaction at a different address must NOT match.
	require.False(t, anyRuleMatches(rules, txEventTo("addr_otheruser")),
		"wallet rule must not match a transaction for an unrelated address")
	// Negative: a block event must NOT match a wallet plan.
	require.False(t, anyRuleMatches(rules, blockEvent("h")),
		"wallet plan must not match block events")
}

// TestWalletRules_DirectionFiltering pins the three wallet matchers'
// per-direction behaviour: a pure-ADA incoming tx fires Incoming
// (not Outgoing, not TokenTransfer); a pure-ADA outgoing tx fires
// Outgoing only; and a token-bearing tx fires Token transfer in
// addition to the directional rule.
func TestWalletRules_DirectionFiltering(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"addr_me"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:     true,
			setup.NotifyPrefOutgoingTx:     true,
			setup.NotifyPrefTokenTransfers: true,
		},
	}
	rules := RulesFromPlan(plan)

	// Helper: which rule IDs match a given event?
	matchingIDs := func(evt event.Event) map[string]bool {
		hit := map[string]bool{}
		for _, r := range rules {
			if r.Matches(evt) {
				hit[r.ID] = true
			}
		}
		return hit
	}

	// Pure-ADA incoming → wallet-in only.
	got := matchingIDs(txEventTo("addr_me"))
	assert.True(t, got["wallet-in"])
	assert.False(t, got["wallet-out"],
		"pure-ADA incoming must NOT fire Outgoing")
	assert.False(t, got["wallet-token"],
		"pure-ADA tx must NOT fire Token transfer")

	// Pure-ADA outgoing → wallet-out only.
	got = matchingIDs(txEventFrom("addr_me"))
	assert.True(t, got["wallet-out"])
	assert.False(t, got["wallet-in"],
		"outgoing tx whose outputs are someone else's must NOT "+
			"fire Incoming")
	assert.False(t, got["wallet-token"],
		"pure-ADA tx must NOT fire Token transfer")

	// Token-bearing incoming → wallet-in AND wallet-token.
	tokenIn := event.Event{
		Type: EventTypeTransaction,
		Payload: map[string]any{
			"outputs": []any{
				map[string]any{
					"address": "addr_me",
					"amount":  uint64(1_000_000),
					"assets": []any{
						map[string]any{
							"policy":      "polA",
							"fingerprint": "asset1abc",
							"quantity":    uint64(1),
						},
					},
				},
			},
		},
	}
	got = matchingIDs(tokenIn)
	assert.True(t, got["wallet-in"])
	assert.True(t, got["wallet-token"],
		"token-bearing incoming tx must fire Token transfer")
	assert.False(t, got["wallet-out"])
}

// TestWalletRules_NoMatchOnUnknownPayloadShape guards the defensive
// shape: a transaction event with no outputs/resolvedInputs (e.g. a
// rollback or schema drift) must not panic and must not match.
func TestWalletRules_NoMatchOnUnknownPayloadShape(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{Wallets: []string{"addr_me"}},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:     true,
			setup.NotifyPrefOutgoingTx:     true,
			setup.NotifyPrefTokenTransfers: true,
		},
	}
	rules := RulesFromPlan(plan)
	cases := []event.Event{
		{Type: EventTypeTransaction, Payload: nil},
		{Type: EventTypeTransaction, Payload: "not a map"},
		{
			Type:    EventTypeTransaction,
			Payload: map[string]any{"transactionHash": "h"},
		},
		{
			Type: EventTypeTransaction,
			Payload: map[string]any{
				"outputs": "not a slice",
			},
		},
	}
	for _, evt := range cases {
		assert.False(t, anyRuleMatches(rules, evt),
			"unknown payload shape must not match")
	}
}

func TestRulesFromPlan_TrackDRep(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			DReps: []string{"drep1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefGovProposals: true,
			setup.NotifyPrefVotesCast:    true,
			setup.NotifyPrefRegChanges:   true,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: a proposal matches the target-independent proposals rule.
	require.True(t, anyRuleMatches(rules, govProposal()),
		"proposals rule should match a proposal event")
	// Positive: governance for the selected DRep matches.
	require.True(t, anyRuleMatches(rules, govEventForDRep("drep1abc")),
		"drep gov rule should match a governance event for the selected DRep")
	require.True(t, anyRuleMatches(rules, govDRepCert("drep1abc")),
		"drep reg rule should match a certificate for the selected DRep")
	// Negative: a governance event for a different DRep must NOT match.
	require.False(t, anyRuleMatches(rules, govEventForDRep("drep1other")),
		"drep gov rule must not match a governance event for another DRep")
	require.False(t, anyRuleMatches(rules, govDRepCert("drep1other")),
		"drep reg rule must not match a certificate for another DRep")
	// Negative: a transaction must NOT match a DRep plan.
	require.False(t, anyRuleMatches(rules, txEvent("h", 1)),
		"drep plan must not match transaction events")
}

func TestRulesFromPlan_MonitorPool(t *testing.T) {
	const poolHex = "aabbccddeeff00112233"
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Pools: []string{poolHex},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefBlocksMinted: true,
			setup.NotifyPrefPoolParams:   true,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: block from the selected pool matches.
	require.True(t, anyRuleMatches(rules, poolBlockEvent("h", poolHex)),
		"pool block rule should match a block event from the selected pool")
	// Negative: a block from a different pool must NOT match.
	require.False(t, anyRuleMatches(rules, poolBlockEvent("h", "ffffffffffffffffffff")),
		"pool block rule must not match a block event from another pool")
	// Negative: governance must NOT match a pool plan.
	require.False(t, anyRuleMatches(rules, govEvent()),
		"pool plan must not match governance events")
}

func TestRulesFromPlan_PoolBech32MatchesOnlyOwnBlocks(t *testing.T) {
	const (
		poolBech32 = "pool16cdtqyk0fvxzfkhjg3esjcuty4tnlpds5lj0lkmqmwdjyzaj7p8"
		poolHash   = "d61ab012cf4b0c24daf2447309638b25573f85b0a7e4ffdb60db9b22"
	)
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{Pools: []string{poolBech32}},
		Notify: setup.NotificationPrefs{setup.NotifyPrefBlocksMinted: true},
	}
	rules := RulesFromPlan(plan)

	require.True(t, anyRuleMatches(rules, poolBlockEvent("h", poolHash)),
		"a block minted by the followed pool must match")
	require.False(t, anyRuleMatches(rules, poolBlockEvent("h",
		"0000000000000000000000000000000000000000000000000000000000")),
		"a block minted by any other pool must NOT match")
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
	require.False(t, anyRuleMatches(rules, ConnectionEvent("Adder Connection", "x")))
}

// TestRulesFromPlan_RollbackRuleIsAlwaysEmitted guards the regression
// where the old inline dispatcher's "input.rollback" case was lost on
// the move to RulesFromPlan: EventTypeRollback was declared but no
// rule ever produced it, so fork-resolution events silently stopped
// surfacing. The rule is always present (independent of
// MonitorEverything and per-target lists) and gated on
// NotifyPrefBlocksMinted to match the old behavior.
func TestRulesFromPlan_RollbackRuleIsAlwaysEmitted(t *testing.T) {
	rollback := event.Event{Type: EventTypeRollback}
	plans := []setup.SetupPlan{
		{Filter: setup.FilterConfig{MonitorEverything: true}},
		{Filter: setup.FilterConfig{Wallets: []string{"a"}}},
		{Filter: setup.FilterConfig{}}, // nothing configured
	}
	for _, p := range plans {
		p.Notify = setup.NotificationPrefs{
			setup.NotifyPrefBlocksMinted: true,
		}
		rules := RulesFromPlan(p)
		assert.True(t, anyRuleMatches(rules, rollback),
			"rollback rule must fire when BlocksMinted=true")
	}
	noToggle := setup.SetupPlan{
		Filter: setup.FilterConfig{MonitorEverything: true},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefBlocksMinted: false,
		},
	}
	assert.False(t,
		anyRuleMatches(RulesFromPlan(noToggle), rollback),
		"rollback rule must be silent when BlocksMinted=false")
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
			setup.NotifyPrefBlocksMinted: true, // block family
			setup.NotifyPrefIncomingTx:   false,
			setup.NotifyPrefVotesCast:    false,
			setup.NotifyPrefPoolParams:   false,
		},
	}
	rules := RulesFromPlan(plan)
	assert.True(t, anyRuleMatches(rules, txEvent("h", 1)),
		"OutgoingTx alone must enable the everything-tx rule")
	assert.True(t, anyRuleMatches(rules, govEvent()),
		"RegChanges alone must enable the everything-gov rule")
	assert.True(t, anyRuleMatches(rules, blockEvent("h")),
		"BlocksMinted alone must enable the everything-block rule")
}

// TestRulesFromPlan_MonitorEverythingPoolParamsAloneDoesNotFireBlockRule
// is the regression guard for the "block-body misleading when only
// PoolParams is on" fix. In MonitorEverything mode the block rule is
// gated on BlocksMinted only — PoolParams is intentionally not in the
// pref list because the everything-block body renders "Block #N
// minted." which is a lie when the trigger was a pool-parameter
// change. The wizard's MonitorEverything pref list also omits
// PoolParams; this test guards the hand-edited / migrated config path
// where PoolParams could still be true.
func TestRulesFromPlan_MonitorEverythingPoolParamsAloneDoesNotFireBlockRule(
	t *testing.T,
) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{MonitorEverything: true},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefPoolParams:   true,
			setup.NotifyPrefBlocksMinted: false,
		},
	}
	rules := RulesFromPlan(plan)
	require.False(t, anyRuleMatches(rules, blockEvent("h")),
		"PoolParams alone must not fire the everything-block rule "+
			"(its body would mislabel pool-param changes as block mints)")
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
	assert.True(t, anyRuleMatches(rules, ConnectionEvent("Adder Connection", "x")),
		"connection rule is independent of per-target lists")
}

// TestRulesFromPlan_ParamsPreservedAndNotMultiplied verifies the
// rule-set carries the WHOLE params list through to Rule.Params on a
// single per-kind rule (one rule per pref, NOT one per param), so a
// later address-aware filter can walk the list to refine matching but
// a single chain event never fires N duplicate notifications when N
// wallets/DReps/pools are configured. Covers each kind independently
// and a combined multi-target plan.
func TestRulesFromPlan_ParamsPreservedAndNotMultiplied(t *testing.T) {
	cases := []struct {
		name       string
		filter     setup.FilterConfig
		pref       string
		wantParams map[string][]string // ruleID -> params
	}{
		{
			name: "wallet",
			filter: setup.FilterConfig{
				Wallets: []string{"addr_test1", "stake_test1"},
			},
			pref: setup.NotifyPrefIncomingTx,
			wantParams: map[string][]string{
				"wallet-in": {"addr_test1", "stake_test1"},
			},
		},
		{
			name: "drep",
			filter: setup.FilterConfig{
				DReps: []string{"drep1abc", "drep1def"},
			},
			pref: setup.NotifyPrefVotesCast,
			wantParams: map[string][]string{
				"drep-votes": {"drep1abc", "drep1def"},
			},
		},
		{
			name: "pool",
			filter: setup.FilterConfig{
				Pools: []string{"pool1abc", "pool1def"},
			},
			pref: setup.NotifyPrefBlocksMinted,
			wantParams: map[string][]string{
				"pool-blocks": {"pool1abc", "pool1def"},
			},
		},
		{
			name: "combined wallet+drep+pool",
			filter: setup.FilterConfig{
				Wallets: []string{"addr1xyz"},
				DReps:   []string{"drep1abc"},
				Pools:   []string{"pool1abc"},
			},
			pref: setup.NotifyPrefIncomingTx,
			wantParams: map[string][]string{
				"wallet-in": {"addr1xyz"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := RulesFromPlan(setup.SetupPlan{
				Filter: tc.filter,
				Notify: setup.NotificationPrefs{tc.pref: true},
			})
			got := map[string][]string{}
			for _, r := range rules {
				if r.Enabled && len(r.Params) > 0 {
					got[r.ID] = r.Params
				}
			}
			assert.Equal(t, tc.wantParams, got,
				"params must be preserved on a single per-kind rule, "+
					"not multiplied into one rule per param")
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

// txWithTokens builds a transaction event whose outputs carry the given
// (policy, fingerprint) pairs. Used by asset/policy matcher tests to
// exercise the payload-walk shape the matchers expect.
func txWithTokens(tokens ...[2]string) event.Event {
	assets := make([]any, 0, len(tokens))
	for _, t := range tokens {
		assets = append(assets, map[string]any{
			"policy":      t[0],
			"fingerprint": t[1],
			"quantity":    uint64(1),
		})
	}
	return event.Event{
		Type: EventTypeTransaction,
		Payload: map[string]any{
			"outputs": []any{
				map[string]any{
					"address": "addr1qxy",
					"amount":  uint64(1000000),
					"assets":  assets,
				},
			},
		},
	}
}

func txToWithTokens(outputAddr string, tokens ...[2]string) event.Event {
	evt := txWithTokens(tokens...)
	outputs := evt.Payload.(map[string]any)["outputs"].([]any)
	outputs[0].(map[string]any)["address"] = outputAddr
	return evt
}

func TestRulesFromPlan_FollowAsset(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Assets: []string{"asset1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefAssetActivity: true,
		},
	}
	rules := RulesFromPlan(plan)

	// Positive: tx with the configured fingerprint matches.
	require.True(t,
		anyRuleMatches(rules, txWithTokens([2]string{"polA", "asset1abc"})),
		"asset rule should match tx with configured fingerprint")
	// Negative: tx with a different fingerprint does NOT match.
	require.False(t,
		anyRuleMatches(rules, txWithTokens([2]string{"polB", "asset1xyz"})),
		"asset rule must not match unrelated fingerprint")
	// Negative: tx with no assets at all does NOT match.
	require.False(t, anyRuleMatches(rules, txEvent("h", 1)),
		"asset rule must not match a plain transfer")
}

func TestRulesFromPlan_FollowPolicy(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Policies: []string{"polA"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefPolicyActivity: true,
		},
	}
	rules := RulesFromPlan(plan)

	require.True(t,
		anyRuleMatches(rules, txWithTokens([2]string{"polA", "asset1abc"})),
		"policy rule should match tx with configured policy ID")
	require.False(t,
		anyRuleMatches(rules, txWithTokens([2]string{"polB", "asset1xyz"})),
		"policy rule must not match unrelated policy ID")
}

// TestMatchAnyAsset_PayloadEdgeCases locks in the documented
// "no panic, no false match" behaviour of the asset matcher when the
// payload is malformed, missing fields, or schema-drifted. Each case
// must return false rather than crashing.
func TestMatchAnyAsset_PayloadEdgeCases(t *testing.T) {
	m := matchAnyAsset([]string{"asset1abc"})

	cases := []struct {
		name string
		evt  event.Event
	}{
		{"non-map payload", event.Event{Payload: "not a map"}},
		{"nil payload", event.Event{}},
		{"missing outputs", event.Event{
			Payload: map[string]any{"other": "x"},
		}},
		{"outputs wrong type", event.Event{
			Payload: map[string]any{"outputs": "not a list"},
		}},
		{"output wrong type", event.Event{
			Payload: map[string]any{"outputs": []any{"not a map"}},
		}},
		{"assets missing", event.Event{
			Payload: map[string]any{
				"outputs": []any{map[string]any{"address": "x"}},
			},
		}},
		{"asset wrong type", event.Event{
			Payload: map[string]any{
				"outputs": []any{map[string]any{
					"assets": []any{"not a map"},
				}},
			},
		}},
		{"fingerprint wrong type", event.Event{
			Payload: map[string]any{
				"outputs": []any{map[string]any{
					"assets": []any{
						map[string]any{"fingerprint": 42},
					},
				}},
			},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.False(t, m(c.evt))
		})
	}
}

// TestRulesFromPlan_CompatibleTargetGroupsANDSemantics guards the UI
// promise for target groups that can coexist on one event: entries
// inside a target section OR together, while populated compatible
// sections AND together.
func TestRulesFromPlan_CompatibleTargetGroupsANDSemantics(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"addr1watch"},
			Assets:  []string{"asset1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:    true,
			setup.NotifyPrefAssetActivity: true,
		},
	}
	rules := RulesFromPlan(plan)

	require.False(t, anyRuleMatches(rules, txEventTo("addr1watch")),
		"wallet-only tx must not match when asset is also configured")
	require.False(t,
		anyRuleMatches(rules, txWithTokens([2]string{"polA", "asset1abc"})),
		"asset-only tx must not match when wallet is also configured")
	require.True(t,
		anyRuleMatches(
			rules,
			txToWithTokens("addr1watch", [2]string{"polA", "asset1abc"}),
		),
		"tx carrying a configured wallet and asset should match")
}

func TestRulesFromPlan_StakeTargetDoesNotBlockAssetActivity(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"stake1uwatched"},
			Assets:  []string{"asset1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefAssetActivity: true,
		},
	}

	require.True(t,
		anyRuleMatches(
			RulesFromPlan(plan),
			txWithTokens([2]string{"polA", "asset1abc"}),
		),
		"stake credentials are not present in transaction payment addresses")
}

func TestRulesFromPlan_StakeTargetDoesNotMatchEveryWalletTransaction(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{Wallets: []string{"stake1uwatched"}},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx: true,
		},
	}

	require.False(t,
		anyRuleMatches(RulesFromPlan(plan), txEventTo("addr1unrelated")),
		"an unsupported stake target must not turn wallet rules into catch-all rules")
}

// TestAllNotifyPrefsHaveEngineRule pins setup.AllNotifyPrefs to the
// rules.go enumeration: for each pref, enabling it (plus a target so
// per-target rules fire) must produce at least one Enabled=true rule.
// If a new pref is added to AllNotifyPrefs without a matching case
// here in rules.go, this test fails — preventing the editor toggle
// from being a silent no-op.
func TestAllNotifyPrefsHaveEngineRule(t *testing.T) {
	// Use one target of every kind so per-target rule families
	// (walletRules, drepRules, poolRules, assetRules, policyRules)
	// have something to fan out over.
	baseFilter := setup.FilterConfig{
		Wallets:  []string{"addr1qxy"},
		DReps:    []string{"drep1abc"},
		Pools:    []string{"pool1xyz"},
		Assets:   []string{"asset1abc"},
		Policies: []string{"polA"},
	}
	for _, pref := range setup.AllNotifyPrefs() {
		t.Run(pref, func(t *testing.T) {
			if pref == setup.NotifyPrefPoolParams {
				t.Skip("adder does not emit a pool-parameter event yet")
			}
			plan := setup.SetupPlan{
				Filter: baseFilter,
				Notify: setup.NotificationPrefs{pref: true},
			}
			rules := RulesFromPlan(plan)
			var enabled int
			for _, r := range rules {
				if r.Enabled {
					enabled++
				}
			}
			require.Greaterf(t, enabled, 0,
				"pref %q produced no enabled rules — add coverage in "+
					"rules.go or remove from setup.AllNotifyPrefs",
				pref)
		})
	}
}
