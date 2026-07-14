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

// Package notifications implements the tray notification engine: it
// consumes events from the tray's ConnectionManager, evaluates them
// against a rule set derived from the user's setup plan, and emits
// notification Requests. The actual desktop dispatch lives in the
// parent tray package; this package only emits to the Requests channel
// that the dispatcher consumes.
package notifications

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/tray/setup"
	lcommon "github.com/blinklabs-io/gouroboros/ledger/common"
)

// Event type constants. The input.* values mirror the event types
// emitted by adder's input plugins (see the event package); the tray.*
// values are synthesized by the engine for non-chain notifications.
const (
	EventTypeBlock       = "input.block"
	EventTypeTransaction = "input.transaction"
	EventTypeGovernance  = "input.governance"
	EventTypeRollback    = "input.rollback"
	// EventTypeConnection is a synthesized event type used to route
	// connection-status notifications through the same rule pipeline as
	// chain events. See Engine.NotifyConnection and ConnectionEvent.
	EventTypeConnection = "tray.connection"
)

// Cardano-aware NotifyBody templates. The {{trunc}}, {{ada}},
// {{outAddr}}, {{outAda}}, and {{field}} funcs are registered by the
// engine at parse time (see formatFuncs in format.go); raw template
// errors fall back to the literal string so a single bad field never
// silently swallows the notification.
const (
	tmplBlockMinted = "Block #{{int .context.blockNumber}} " +
		"({{truncHash .payload.blockHash}}) minted."
	tmplPoolBlock = "Pool {{trunc .payload.issuerVkey}} " +
		"minted block #{{int .context.blockNumber}} " +
		"({{truncHash .payload.blockHash}})."
	tmplTxGeneric = "Transaction {{trunc .context.transactionHash}}."
	// tmplTxReceived renders the ADA sum landing on the WATCHED
	// address (not the whole tx amount, which would include change
	// going back to the sender).
	tmplTxReceived = "Received {{mineAda .payload.outputs .params}} " +
		"ADA at {{mine .payload.outputs .params}}."
	// tmplTxSent renders the ADA leaving the wallet to the
	// counterparty's first output, excluding change.
	tmplTxSent = "Sent {{otherAda .payload.outputs .params}} ADA " +
		"to {{other .payload.outputs .params}}."
	// tmplTxToken renders the watched address that received tokens
	// when the matching side is incoming, falling back to the
	// counterparty when the tx is outgoing.
	tmplTxToken = "Token transfer at " +
		"{{or (mine .payload.outputs .params) " +
		"(other .payload.outputs .params)}}."
	// voteFor selects the voting procedure cast by a followed DRep
	// (.params) so an event carrying several votes renders the followed
	// DRep's vote, not whichever happens to be first.
	tmplGovVote = "DRep " +
		"{{trunc (field \"voterId\" (voteFor .payload.votingProcedures .params))}} " +
		"voted {{field \"vote\" (voteFor .payload.votingProcedures .params)}} " +
		"on proposal " +
		"#{{field \"govActionIndex\" (voteFor .payload.votingProcedures .params)}}."
	tmplGovProposal   = "New governance proposal detected."
	tmplGovReg        = "A registration change was detected."
	tmplAssetActivity = "Asset activity detected in tx " +
		"{{trunc .context.transactionHash}}."
	tmplPolicyActivity = "Policy activity detected in tx " +
		"{{trunc .context.transactionHash}}."
)

// Rule describes a single notification rule. Rules are derived from the
// user's setup plan (template + parameters + notification preferences)
// by RulesFromPlan.
type Rule struct {
	// ID uniquely identifies the rule within a rule set.
	ID string
	// Enabled gates the rule; a disabled rule never matches.
	Enabled bool
	// EventType is the event.Event.Type this rule applies to, e.g.
	// "input.transaction" or "tray.connection".
	EventType string
	// MatchExpr is an optional simple dotted-path equality match against
	// the event payload/context (Phase 1: no JMESPath). An empty MatchExpr
	// matches any event of the rule's EventType. Format:
	// "payload.blockHash=abc" or "context.networkMagic=2". A malformed
	// expression (no '=') never matches.
	MatchExpr string
	// Params carries the template parameter values this rule covers
	// (wallet addresses, DRep IDs, pool IDs). One rule per (kind,
	// pref) — so the firing fan-out is bounded by prefs, not by
	// param count. Empty for rules that aren't template-parameterised
	// (Monitor Everything, connection, rollback).
	Params []string
	// NotifyTitle is a template string for the notification title.
	NotifyTitle string
	// NotifyBody is a template string for the notification body.
	NotifyBody string

	// CustomMatch is a Go-side matcher used INSTEAD of MatchExpr when
	// the match shape exceeds dotted-path equality (e.g. asset/policy
	// rules walking payload.outputs[*].assets). Mutually exclusive
	// with MatchExpr.
	CustomMatch func(event.Event) bool

	// titleTmpl/bodyTmpl are pre-compiled forms of NotifyTitle/Body.
	// nil when the source has no `{{` or failed to parse — render
	// falls back to the raw text.
	titleTmpl, bodyTmpl *template.Template
}

// Matches reports whether the rule applies to evt: enabled, event
// type matches, and CustomMatch (when set) or MatchExpr evaluates
// true. CustomMatch wins so the two paths can't combine into
// surprising AND semantics.
func (r Rule) Matches(evt event.Event) bool {
	if !r.Enabled {
		return false
	}
	if r.EventType != evt.Type {
		return false
	}
	if r.CustomMatch != nil {
		return r.CustomMatch(evt)
	}
	return evalMatchExpr(r.MatchExpr, evt)
}

// evalMatchExpr evaluates a simple "section.path...=value" expression
// against an event. An empty expression always matches. A malformed
// expression (missing '=') never matches. The leading section selects
// the event field to walk: "payload" or "context". JSON-decoded numbers
// arrive as float64, so numeric leaves are compared by their canonical
// string form.
func evalMatchExpr(expr string, evt event.Event) bool {
	if expr == "" {
		return true
	}
	key, want, found := strings.Cut(expr, "=")
	if !found {
		return false
	}
	section, path, hasPath := strings.Cut(key, ".")
	if !hasPath {
		return false
	}

	var root any
	switch section {
	case "payload":
		root = evt.Payload
	case "context":
		root = evt.Context
	default:
		return false
	}

	got, ok := lookupPath(root, path)
	if !ok {
		return false
	}
	return valueToString(got) == want
}

// lookupPath walks a dotted path through nested map[string]any (the
// shape produced by json.Unmarshal into an `any`). It returns the leaf
// value and whether the full path was resolved.
func lookupPath(root any, path string) (any, bool) {
	cur := root
	for seg := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// valueToString renders a leaf value for comparison. JSON numbers decode
// to float64; integral values are rendered without a trailing ".0" so
// "context.networkMagic=2" matches a decoded 2.0.
func valueToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// RulesFromPlan derives the active rule set from a setup plan. The
// mapping mirrors the wizard's notification preferences (see
// tray/wizard/step4_notifications.go) and the existing inline dispatch
// logic in tray/app.go, so this engine can replace that inline path.
//
// Values inside each target group OR together. The configured connectors join
// adjacent populated groups with AND or OR. Event families that cannot carry
// the same fields naturally fail expressions requiring those fields. When
// MonitorEverything is on, targets are ignored and a single coarse rule per
// event type is emitted.
//
// Assumption (documented): several preferences are finer-grained than
// the event types adder emits. Incoming/Outgoing/Token-transfer all
// collapse to "input.transaction" (the transaction event carries no
// in/out direction flag), and Pool-parameter/Registration changes
// collapse to their coarse event type. Those prefs therefore act as
// independent enable toggles over the same coarse event-type rule
// rather than distinct payload matches. The wallet/DRep/pool parameter
// itself is preserved verbatim on each rule's Param field so a later
// address-aware filter can refine matching without reshaping the rule
// model.
func RulesFromPlan(plan setup.SetupPlan) []Rule {
	var rules []Rule
	prefs := plan.Notify

	if plan.Filter.MonitorEverything {
		rules = append(rules, everythingRules(prefs)...)
	} else {
		targetRules := make([]Rule, 0,
			len(plan.Filter.Wallets)+len(plan.Filter.DReps)+
				len(plan.Filter.Pools)+len(plan.Filter.Assets)+
				len(plan.Filter.Policies))
		targetRules = append(targetRules,
			walletRules(prefs, plan.Filter.Wallets)...)
		targetRules = append(targetRules,
			drepRules(prefs, plan.Filter.DReps)...)
		targetRules = append(targetRules,
			poolRules(prefs, plan.Filter.Pools)...)
		targetRules = append(targetRules,
			assetRules(prefs, plan.Filter.Assets)...)
		targetRules = append(targetRules,
			policyRules(prefs, plan.Filter.Policies)...)
		rules = append(rules,
			applyStandardExpression(targetRules, plan.Filter)...)
	}

	// Rollback fires for fork resolutions regardless of monitored
	// targets, gated on the blocks-minted pref.
	rules = append(rules, Rule{
		ID:          "rollback",
		Enabled:     prefs[setup.NotifyPrefBlocksMinted],
		EventType:   EventTypeRollback,
		NotifyTitle: "🔄 Chain Rollback",
		NotifyBody:  "A chain rollback was detected.",
	})

	// Connection rule is target-independent and routed via
	// Engine.NotifyConnection. Title/body come from the synthesized
	// event so the observer can pass severity-specific labels.
	rules = append(rules, Rule{
		ID:          "connection",
		Enabled:     prefs[setup.NotifyPrefConnectionIssues],
		EventType:   EventTypeConnection,
		NotifyTitle: "{{.payload.title}}",
		NotifyBody:  "{{.payload.message}}",
	})

	return rules
}

func applyStandardExpression(
	rules []Rule,
	filter setup.FilterConfig,
) []Rule {
	expression := standardFilterMatcher(filter)
	if expression == nil {
		return rules
	}
	for i := range rules {
		ownMatch := rules[i].CustomMatch
		if ownMatch == nil {
			// Defensive: every current target rule sets CustomMatch, but a
			// future rule without one would panic in the closure below.
			// Fall back to the combined expression alone.
			rules[i].CustomMatch = expression
			continue
		}
		rules[i].CustomMatch = func(evt event.Event) bool {
			return ownMatch(evt) && expression(evt)
		}
	}
	return rules
}

func standardFilterMatcher(
	filter setup.FilterConfig,
) func(event.Event) bool {
	type joinedCondition struct {
		match func(event.Event) bool
		join  setup.AdvancedMatchMode
	}
	var conditions []joinedCondition
	if len(filter.Wallets) > 0 {
		addresses := stringSet(filter.Wallets)
		conditions = append(conditions, joinedCondition{
			match: func(evt event.Event) bool {
				return matchAnyOutputAddress(addresses)(evt) ||
					matchAnyResolvedInputAddress(addresses)(evt)
			},
			join: setup.AdvancedMatchAny,
		})
	}
	if len(filter.DReps) > 0 {
		conditions = append(conditions, joinedCondition{
			match: matchDRepActivity(filter.DReps),
			join:  filter.ResolvedDRepMatch(),
		})
	}
	if len(filter.Pools) > 0 {
		conditions = append(conditions, joinedCondition{
			match: matchBlockIssuer(filter.Pools),
			join:  filter.ResolvedPoolMatch(),
		})
	}
	if len(filter.Assets) > 0 {
		conditions = append(conditions, joinedCondition{
			match: matchAnyAsset(filter.Assets),
			join:  filter.ResolvedAssetMatch(),
		})
	}
	if len(filter.Policies) > 0 {
		conditions = append(conditions, joinedCondition{
			match: matchAnyPolicy(filter.Policies),
			join:  filter.ResolvedPolicyMatch(),
		})
	}
	if len(conditions) == 0 {
		return nil
	}
	return func(evt event.Event) bool {
		groupMatches := conditions[0].match(evt)
		for _, condition := range conditions[1:] {
			if condition.join == setup.AdvancedMatchAny {
				if groupMatches {
					return true
				}
				groupMatches = condition.match(evt)
			} else {
				groupMatches = groupMatches && condition.match(evt)
			}
		}
		return groupMatches
	}
}

func matchDRepActivity(dreps []string) func(event.Event) bool {
	set := lowerSet(dreps)
	votes := matchGovIdentity(
		"votingProcedures", "voterId", "voterHash", set,
	)
	certificates := matchGovIdentity(
		"drepCertificates", "drepId", "drepHash", set,
	)
	proposals := matchHasEntries("proposalProcedures")
	return func(evt event.Event) bool {
		return votes(evt) || certificates(evt) || proposals(evt)
	}
}

// coarseRule describes one of the broad event-family rules emitted in
// MonitorEverything mode. The rule is Enabled if ANY of the listed
// prefs is on, so toggling e.g. only "Outgoing transactions" still
// produces a tx notification.
type coarseRule struct {
	id, eventType, title, body string
	prefs                      []string
}

// everythingMode drives MonitorEverything. Bodies use the
// Cardano-aware template helpers registered by the engine.
var everythingMode = []coarseRule{
	{
		// PoolParams is NOT listed: MonitorEverything receives block
		// events, not pool-parameter-change events, so firing this
		// rule on PoolParams would mislabel every block as
		// pool-parameter activity.
		id: "everything-block", eventType: EventTypeBlock,
		title: "🧱 New Block",
		body:  tmplBlockMinted,
		prefs: []string{
			setup.NotifyPrefBlocksMinted,
		},
	},
	{
		id: "everything-tx", eventType: EventTypeTransaction,
		title: "💸 New Transaction",
		body:  tmplTxGeneric,
		prefs: []string{
			setup.NotifyPrefIncomingTx,
			setup.NotifyPrefOutgoingTx,
			setup.NotifyPrefTokenTransfers,
		},
	},
	{
		id: "everything-gov", eventType: EventTypeGovernance,
		title: "🗳️ Governance Action",
		body:  tmplGovVote,
		prefs: []string{
			setup.NotifyPrefGovProposals,
			setup.NotifyPrefVotesCast,
			setup.NotifyPrefRegChanges,
		},
	},
}

// everythingRules materialises the MonitorEverything coarse rule set
// from the everythingMode table, ORing the relevant prefs to decide
// each rule's Enabled flag.
func everythingRules(prefs setup.NotificationPrefs) []Rule {
	out := make([]Rule, 0, len(everythingMode))
	for _, c := range everythingMode {
		enabled := false
		for _, k := range c.prefs {
			if prefs[k] {
				enabled = true
				break
			}
		}
		out = append(out, Rule{
			ID:          c.id,
			Enabled:     enabled,
			EventType:   c.eventType,
			NotifyTitle: c.title,
			NotifyBody:  c.body,
		})
	}
	return out
}

// walletRules builds transaction rules for the followed wallets. Each
// rule uses a CustomMatch closure so the three classes only fire on
// genuinely-matching transactions:
//
//   - Incoming: any followed address appears in payload.outputs[*]
//   - Outgoing: any followed address appears in
//     payload.resolvedInputs[*] (the spent UTxOs)
//   - Token transfer: any followed address is involved (either side)
//     AND the transaction carries at least one native-token output
//
// Without these filters every wallet pref fired on every transaction
// event the engine received, producing up to three identical
// notifications per tx regardless of direction or asset content.
func walletRules(prefs setup.NotificationPrefs, params []string) []Rule {
	if len(params) == 0 {
		return nil
	}
	addrs := append([]string(nil), params...)
	addrSet := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		addrSet[a] = struct{}{}
	}
	return []Rule{
		{
			ID:          "wallet-in",
			Enabled:     prefs[setup.NotifyPrefIncomingTx],
			EventType:   EventTypeTransaction,
			Params:      addrs,
			NotifyTitle: "💸 Incoming Transaction",
			NotifyBody:  tmplTxReceived,
			CustomMatch: matchAnyOutputAddress(addrSet),
		},
		{
			ID:          "wallet-out",
			Enabled:     prefs[setup.NotifyPrefOutgoingTx],
			EventType:   EventTypeTransaction,
			Params:      addrs,
			NotifyTitle: "💸 Outgoing Transaction",
			NotifyBody:  tmplTxSent,
			CustomMatch: matchAnyResolvedInputAddress(addrSet),
		},
		{
			ID:          "wallet-token",
			Enabled:     prefs[setup.NotifyPrefTokenTransfers],
			EventType:   EventTypeTransaction,
			Params:      addrs,
			NotifyTitle: "🪙 Token Transfer",
			NotifyBody:  tmplTxToken,
			CustomMatch: matchWalletTokenTransfer(addrSet),
		},
	}
}

// matchAnyOutputAddress fires when any payload.outputs[*].address is
// in the followed wallet set.
func matchAnyOutputAddress(addrs map[string]struct{}) func(event.Event) bool {
	return func(evt event.Event) bool {
		return anyOutputField(evt, func(out map[string]any) bool {
			addr, _ := out["address"].(string)
			_, ok := addrs[addr]
			return ok
		})
	}
}

// matchAnyResolvedInputAddress fires when any
// payload.resolvedInputs[*].address is in the followed wallet set.
// ResolvedInputs is omitempty on the upstream event, so it may be
// absent for transactions where the tx producer did not resolve the
// spent UTxOs; in that case the matcher returns false rather than
// guessing the direction.
func matchAnyResolvedInputAddress(
	addrs map[string]struct{},
) func(event.Event) bool {
	return func(evt event.Event) bool {
		return anyResolvedInputField(evt, func(in map[string]any) bool {
			addr, _ := in["address"].(string)
			_, ok := addrs[addr]
			return ok
		})
	}
}

// matchWalletTokenTransfer fires when any followed address is involved
// (incoming OR outgoing) AND the transaction carries at least one
// native-token output. Orthogonal to Incoming/Outgoing: enabling
// Token transfers alongside one of them will produce two notifications
// for a token-bearing tx — intentional, the second message carries the
// extra signal "this wasn't pure ADA".
func matchWalletTokenTransfer(
	addrs map[string]struct{},
) func(event.Event) bool {
	addrMatch := func(m map[string]any) bool {
		addr, _ := m["address"].(string)
		_, ok := addrs[addr]
		return ok
	}
	return func(evt event.Event) bool {
		involved := anyOutputField(evt, addrMatch) ||
			anyResolvedInputField(evt, addrMatch)
		if !involved {
			return false
		}
		return anyOutputToken(evt, func(map[string]any) bool {
			return true
		})
	}
}

// anyOutputField walks payload.outputs[*] and returns true when match
// reports true for any output. Same defensive shape as anyOutputToken.
func anyOutputField(
	evt event.Event,
	match func(map[string]any) bool,
) bool {
	return anyMapEntry(evt, "outputs", match)
}

// anyResolvedInputField walks payload.resolvedInputs[*] and returns
// true when match reports true for any entry.
func anyResolvedInputField(
	evt event.Event,
	match func(map[string]any) bool,
) bool {
	return anyMapEntry(evt, "resolvedInputs", match)
}

// anyMapEntry is the generic walker the *Field helpers share — pulls
// payload[key] as []any and feeds each map element to match.
func anyMapEntry(
	evt event.Event,
	key string,
	match func(map[string]any) bool,
) bool {
	payload, ok := evt.Payload.(map[string]any)
	if !ok {
		return false
	}
	entries, ok := payload[key].([]any)
	if !ok {
		return false
	}
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if match(m) {
			return true
		}
	}
	return false
}

// drepRules builds governance rules scoped to the followed DReps. A
// governance event is a single "input.governance" type carrying any of
// proposals, votes, and DRep certificates, so each rule uses a
// CustomMatch that (a) checks the event actually contains its subtype
// and (b) for votes/registrations, that a followed DRep is the actor —
// otherwise every rule fired on every governance event regardless of
// DRep, notifying the user about the whole network's activity.
//
// Proposals are not attributable to a specific DRep (anyone may create
// one), so the proposals rule fires on any governance event that carries
// a proposal — a call to action for the DReps the user follows.
func drepRules(prefs setup.NotificationPrefs, params []string) []Rule {
	if len(params) == 0 {
		return nil
	}
	snap := append([]string(nil), params...)
	set := lowerSet(snap)
	return []Rule{
		{
			ID:          "drep-proposals",
			Enabled:     prefs[setup.NotifyPrefGovProposals],
			EventType:   EventTypeGovernance,
			Params:      snap,
			NotifyTitle: "🗳️ Governance Proposal",
			NotifyBody:  tmplGovProposal,
			CustomMatch: matchHasEntries("proposalProcedures"),
		},
		{
			ID:          "drep-votes",
			Enabled:     prefs[setup.NotifyPrefVotesCast],
			EventType:   EventTypeGovernance,
			Params:      snap,
			NotifyTitle: "🗳️ Vote Cast",
			NotifyBody:  tmplGovVote,
			CustomMatch: matchGovIdentity(
				"votingProcedures", "voterId", "voterHash", set),
		},
		{
			ID:          "drep-reg",
			Enabled:     prefs[setup.NotifyPrefRegChanges],
			EventType:   EventTypeGovernance,
			Params:      snap,
			NotifyTitle: "🗳️ Registration Change",
			NotifyBody:  tmplGovReg,
			CustomMatch: matchGovIdentity(
				"drepCertificates", "drepId", "drepHash", set),
		},
	}
}

// poolRules builds a block rule scoped to the followed pools: it fires
// only when the block's issuer is one of them. Without the issuer match
// the rule fired on every block on the chain, not just the user's pool.
//
// There is intentionally no pool-parameter rule: adder emits no
// pool-parameter-change event, so the old "params" rule was mapped onto
// block events and simply fired (mislabeled) on every block.
func poolRules(prefs setup.NotificationPrefs, params []string) []Rule {
	if len(params) == 0 {
		return nil
	}
	snap := append([]string(nil), params...)
	return []Rule{{
		ID:          "pool-blocks",
		Enabled:     prefs[setup.NotifyPrefBlocksMinted],
		EventType:   EventTypeBlock,
		Params:      snap,
		NotifyTitle: "🧱 Block Minted",
		NotifyBody:  tmplPoolBlock,
		CustomMatch: matchBlockIssuer(snap),
	}}
}

// lowerSet returns a set of the lowercased input strings.
func lowerSet(vals []string) map[string]struct{} {
	set := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		set[strings.ToLower(v)] = struct{}{}
	}
	return set
}

// stringSet returns a set of the input strings matched exactly, without
// case folding. Used for wallet addresses and asset fingerprints, which
// are bech32 and lowercase-only by spec. Pool/DRep IDs (lowerSet) and
// policy IDs (matchAnyPolicy) fold case instead, since they may be
// entered as mixed-case hex.
func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

// matchBlockIssuer fires when a block's issuer is one of the followed
// pools. Block events carry payload.issuerVkey as the hex pool key-hash
// (block.IssuerVkey().Hash()); configured pool IDs may be bech32
// ("pool1…") or that same hex, so bech32 IDs are decoded to hex before
// comparison.
func matchBlockIssuer(pools []string) func(event.Event) bool {
	set := make(map[string]struct{}, len(pools))
	for _, p := range pools {
		if strings.HasPrefix(p, "pool1") {
			if id, err := lcommon.NewPoolIdFromBech32(p); err == nil {
				set[hex.EncodeToString(id[:])] = struct{}{}
			}
			continue
		}
		set[strings.ToLower(p)] = struct{}{}
	}
	return func(evt event.Event) bool {
		payload, ok := evt.Payload.(map[string]any)
		if !ok {
			return false
		}
		issuer, _ := payload["issuerVkey"].(string)
		_, ok = set[strings.ToLower(issuer)]
		return ok
	}
}

// matchGovIdentity fires when any entry under payload[key] names a
// followed target in either its bech32 id field or its hex hash field
// (the user may configure DReps as "drep1…" or as hex).
func matchGovIdentity(
	key, idField, hashField string, set map[string]struct{},
) func(event.Event) bool {
	return func(evt event.Event) bool {
		return anyMapEntry(evt, key, func(m map[string]any) bool {
			id, _ := m[idField].(string)
			if _, ok := set[strings.ToLower(id)]; ok {
				return true
			}
			h, _ := m[hashField].(string)
			_, ok := set[strings.ToLower(h)]
			return ok
		})
	}
}

// matchHasEntries fires when payload[key] is a non-empty list, i.e. the
// governance event actually carries that subtype.
func matchHasEntries(key string) func(event.Event) bool {
	return func(evt event.Event) bool {
		payload, ok := evt.Payload.(map[string]any)
		if !ok {
			return false
		}
		entries, ok := payload[key].([]any)
		return ok && len(entries) > 0
	}
}

// assetRules builds one rule that fires on any transaction touching
// a followed asset fingerprint. Uses a CustomMatch closure because the
// match walks payload.outputs[*].assets[*].fingerprint.
func assetRules(prefs setup.NotificationPrefs, assets []string) []Rule {
	if len(assets) == 0 {
		return nil
	}
	snap := append([]string(nil), assets...)
	return []Rule{{
		ID:          "asset-activity",
		Enabled:     prefs[setup.NotifyPrefAssetActivity],
		EventType:   EventTypeTransaction,
		Params:      snap,
		NotifyTitle: "🪙 Asset Activity",
		NotifyBody:  tmplAssetActivity,
		CustomMatch: matchAnyAsset(snap),
	}}
}

// policyRules builds one rule that fires on any transaction touching
// one of the followed minting-policy IDs. Same shape as assetRules,
// matching on payload.outputs[*].assets[*].policy.
func policyRules(prefs setup.NotificationPrefs, policies []string) []Rule {
	if len(policies) == 0 {
		return nil
	}
	snap := append([]string(nil), policies...)
	return []Rule{{
		ID:          "policy-activity",
		Enabled:     prefs[setup.NotifyPrefPolicyActivity],
		EventType:   EventTypeTransaction,
		Params:      snap,
		NotifyTitle: "🪙 Policy Activity",
		NotifyBody:  tmplPolicyActivity,
		CustomMatch: matchAnyPolicy(snap),
	}}
}

// matchAnyAsset returns a CustomMatch that fires when any tx output
// carries a token whose fingerprint is in the configured set. Walks
// payload.outputs[*].assets[*] and returns false on any malformed
// field — schema drift must not panic the engine.
func matchAnyAsset(assets []string) func(event.Event) bool {
	set := make(map[string]struct{}, len(assets))
	for _, a := range assets {
		set[a] = struct{}{}
	}
	return func(evt event.Event) bool {
		return anyOutputToken(evt, func(tok map[string]any) bool {
			fp, _ := tok["fingerprint"].(string)
			_, ok := set[fp]
			return ok
		})
	}
}

// matchAnyPolicy is the policy-ID counterpart of matchAnyAsset. Policy
// IDs are hex script hashes and hex.DecodeString is case-insensitive, so
// a validly-entered uppercase/mixed-case ID must still match the chain's
// lowercase policy field. Fold both sides to lower, as matchBlockIssuer
// and matchGovIdentity do for pool/DRep IDs.
func matchAnyPolicy(policies []string) func(event.Event) bool {
	set := make(map[string]struct{}, len(policies))
	for _, p := range policies {
		set[strings.ToLower(p)] = struct{}{}
	}
	return func(evt event.Event) bool {
		return anyOutputToken(evt, func(tok map[string]any) bool {
			pol, _ := tok["policy"].(string)
			_, ok := set[strings.ToLower(pol)]
			return ok
		})
	}
}

// anyOutputToken walks payload.outputs[*].assets[*] and returns true
// when matchTok returns true for any token.
func anyOutputToken(
	evt event.Event,
	matchTok func(map[string]any) bool,
) bool {
	payload, ok := evt.Payload.(map[string]any)
	if !ok {
		return false
	}
	outputs, ok := payload["outputs"].([]any)
	if !ok {
		return false
	}
	for _, o := range outputs {
		out, ok := o.(map[string]any)
		if !ok {
			continue
		}
		tokens, ok := out["assets"].([]any)
		if !ok {
			continue
		}
		for _, t := range tokens {
			tok, ok := t.(map[string]any)
			if !ok {
				continue
			}
			if matchTok(tok) {
				return true
			}
		}
	}
	return false
}
