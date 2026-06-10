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
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/tray/setup"
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
	// Param is the template parameter this rule was derived from (the
	// wallet address, DRep ID, or pool ID). It is preserved verbatim from
	// the setup plan so an address-aware filter can refine matching
	// without reshaping the rule model. Empty for rules that are not
	// template-parameterised (e.g. "Monitor Everything", connection).
	Param string
	// NotifyTitle is a template string for the notification title.
	NotifyTitle string
	// NotifyBody is a template string for the notification body.
	NotifyBody string

	// titleTmpl and bodyTmpl are pre-compiled forms of NotifyTitle and
	// NotifyBody. NewEngine populates them once at construction so the
	// hot path (one Execute per match) does not re-parse the template
	// on every event. Either may be nil if the source string has no
	// `{{` or failed to parse — render falls back to the raw text.
	titleTmpl, bodyTmpl *template.Template
}

// Matches reports whether the rule applies to the given event: the rule
// must be enabled, the event type must match, and the MatchExpr (if any)
// must evaluate true against the event.
func (r Rule) Matches(evt event.Event) bool {
	if !r.Enabled {
		return false
	}
	if r.EventType != evt.Type {
		return false
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
	for _, seg := range strings.Split(path, ".") {
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
// The wizard lets the user populate the three per-target lists
// (Wallets, DReps, Pools) independently — and this function fans out a
// rule per entry in each list, giving natural OR semantics across the
// kinds. When MonitorEverything is on the per-target lists are ignored
// and a single coarse rule per event type is emitted instead.
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
		rules = append(rules,
			walletRules(prefs, plan.Filter.Wallets)...)
		rules = append(rules,
			drepRules(prefs, plan.Filter.DReps)...)
		rules = append(rules,
			poolRules(prefs, plan.Filter.Pools)...)
	}

	// Connection-status rule is independent of the user's targets and
	// routed via Engine.NotifyConnection through the synthesized
	// connection event.
	rules = append(rules, Rule{
		ID:          "connection",
		Enabled:     prefs[setup.NotifyPrefConnectionIssues],
		EventType:   EventTypeConnection,
		NotifyTitle: "Adder Connection",
		NotifyBody:  "{{.payload.message}}",
	})

	return rules
}

// templatedRule describes one (preference, suffix, title, body) tuple
// that is fanned out per parameter value by perParam.
type templatedRule struct {
	pref, suffix, title, body string
}

// coarseRule describes one of the broad event-family rules emitted in
// MonitorEverything mode. The rule is Enabled if ANY of the listed
// prefs is on, so toggling e.g. only "Outgoing transactions" still
// produces a tx notification.
type coarseRule struct {
	id, eventType, title, body string
	prefs                      []string
}

// everythingMode is the table that drives MonitorEverything. Kept next
// to the templatedRule tables below so rewordings/pref bindings live in
// one place per branch instead of split between a closure and a table.
var everythingMode = []coarseRule{
	{
		id: "everything-block", eventType: EventTypeBlock,
		title: "🧱 New Block",
		body:  "A new block has been minted.",
		prefs: []string{
			setup.NotifyPrefBlocksMinted,
			setup.NotifyPrefPoolParams,
		},
	},
	{
		id: "everything-tx", eventType: EventTypeTransaction,
		title: "💸 New Transaction",
		body:  "A new transaction was detected.",
		prefs: []string{
			setup.NotifyPrefIncomingTx,
			setup.NotifyPrefOutgoingTx,
			setup.NotifyPrefTokenTransfers,
		},
	},
	{
		id: "everything-gov", eventType: EventTypeGovernance,
		title: "🗳️ Governance Action",
		body:  "A new governance action was detected.",
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

// perParam fans out a set of templated rules across the supplied params,
// emitting one Rule per (param, def) pair. The parameter value is
// preserved verbatim on Rule.Param so a later address-aware filter can
// refine matching without reshaping the rule model. Empty params
// returns nil so an unconfigured target section never produces a
// catch-all rule that would match every event of the kind.
func perParam(
	idPrefix, eventType string,
	prefs setup.NotificationPrefs,
	params []string,
	defs []templatedRule,
) []Rule {
	if len(params) == 0 {
		return nil
	}
	rules := make([]Rule, 0, len(params)*len(defs))
	for i, p := range params {
		for _, d := range defs {
			rules = append(rules, Rule{
				ID:          fmt.Sprintf("%s-%s-%d", idPrefix, d.suffix, i),
				Enabled:     prefs[d.pref],
				EventType:   eventType,
				Param:       p,
				NotifyTitle: d.title,
				NotifyBody:  d.body,
				// Phase 1: coarse type match. Address-aware matching
				// (consuming Param) is a future enhancement.
			})
		}
	}
	return rules
}

// walletRules builds transaction rules for the Watch Wallet template:
// one rule per parameter value per enabled transaction preference, so
// each (address, pref) pair is independently addressable by a later
// address-aware filter.
func walletRules(prefs setup.NotificationPrefs, params []string) []Rule {
	return perParam("wallet", EventTypeTransaction, prefs, params,
		[]templatedRule{
			{
				setup.NotifyPrefIncomingTx, "in",
				"💸 Incoming Transaction",
				"An incoming transaction was detected.",
			},
			{
				setup.NotifyPrefOutgoingTx, "out",
				"💸 Outgoing Transaction",
				"An outgoing transaction was detected.",
			},
			{
				setup.NotifyPrefTokenTransfers, "token",
				"💸 Token Transfer",
				"A token transfer was detected.",
			},
		},
	)
}

// drepRules builds governance rules for the Track DRep template: one
// rule per DRep ID per enabled governance preference, so each (DRep ID,
// pref) pair is independently addressable by a later address-aware
// filter.
func drepRules(prefs setup.NotificationPrefs, params []string) []Rule {
	return perParam("drep", EventTypeGovernance, prefs, params,
		[]templatedRule{
			{
				setup.NotifyPrefGovProposals, "proposals",
				"🗳️ Governance Proposal",
				"A new governance proposal was detected.",
			},
			{
				setup.NotifyPrefVotesCast, "votes",
				"🗳️ Vote Cast",
				"A governance vote was cast.",
			},
			{
				setup.NotifyPrefRegChanges, "reg",
				"🗳️ Registration Change",
				"A registration change was detected.",
			},
		},
	)
}

// poolRules builds block-event rules for the Monitor Pool template: one
// rule per pool ID per enabled pool preference, so each (pool ID, pref)
// pair is independently addressable by a later address-aware filter.
func poolRules(prefs setup.NotificationPrefs, params []string) []Rule {
	return perParam("pool", EventTypeBlock, prefs, params,
		[]templatedRule{
			{
				setup.NotifyPrefBlocksMinted, "blocks",
				"🧱 Block Minted",
				"A new block has been minted.",
			},
			{
				setup.NotifyPrefPoolParams, "params",
				"🧱 Pool Parameter Change",
				"A pool parameter change was detected.",
			},
		},
	)
}
