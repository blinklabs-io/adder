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
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

const (
	// NotifyPrefBlocksMinted is the preference key for block minting alerts.
	NotifyPrefBlocksMinted = "Blocks minted"
	// NotifyPrefIncomingTx is the preference key for incoming transaction alerts.
	NotifyPrefIncomingTx = "Incoming transactions"
	// NotifyPrefOutgoingTx is the preference key for outgoing transaction alerts.
	NotifyPrefOutgoingTx = "Outgoing transactions"
	// NotifyPrefTokenTransfers is the preference key for token transfer alerts.
	NotifyPrefTokenTransfers = "Token transfers"
	// NotifyPrefGovProposals is the preference key for new governance proposals.
	NotifyPrefGovProposals = "New governance proposals"
	// NotifyPrefVotesCast is the preference key for votes cast alerts.
	NotifyPrefVotesCast = "Votes cast"
	// NotifyPrefRegChanges is the preference key for registration change alerts.
	NotifyPrefRegChanges = "Registration changes"
	// NotifyPrefAssetActivity is the preference key for events touching
	// any followed asset fingerprint (mint, burn, transfer).
	NotifyPrefAssetActivity = "Asset activity"
	// NotifyPrefPolicyActivity is the preference key for events touching
	// any followed minting policy ID.
	NotifyPrefPolicyActivity = "Policy activity"
	// NotifyPrefConnectionIssues is the preference key for connection status
	// alerts.
	NotifyPrefConnectionIssues = "Connection issues"
)

// allNotifyPrefs is the canonical ordering of every NotifyPref* used
// across the rules editor, the wizard's notifications step, and rule
// derivation. Order groups chain activity first, then governance,
// then asset/policy, then connection. Unexported so importers cannot
// reassign or mutate in place — use AllNotifyPrefs() to read.
var allNotifyPrefs = []string{
	NotifyPrefIncomingTx,
	NotifyPrefOutgoingTx,
	NotifyPrefTokenTransfers,
	NotifyPrefBlocksMinted,
	NotifyPrefGovProposals,
	NotifyPrefVotesCast,
	NotifyPrefRegChanges,
	NotifyPrefAssetActivity,
	NotifyPrefPolicyActivity,
	NotifyPrefConnectionIssues,
}

// AllNotifyPrefs returns the canonical ordering of every NotifyPref*
// as a fresh slice; the backing array is private so callers cannot
// corrupt the order. New prefs must be added to allNotifyPrefs so
// every surface enumerates the same set; TestAllNotifyPrefsExhaustive
// guards against drift between this list and the NotifyPref* consts.
func AllNotifyPrefs() []string {
	return slices.Clone(allNotifyPrefs)
}

// SetupPlan represents the desired configuration state of the Adder ecosystem,
// decoupled from UI display strings and engine-specific map structures.
type SetupPlan struct {
	Network NetworkConfig
	Filter  FilterConfig
	Output  OutputConfig
	API     APIConfig
	Notify  NotificationPrefs
	App     AppConfig
}

// NetworkConfig defines the Cardano network settings.
type NetworkConfig struct {
	Name          string // mainnet, preprod, preview
	CustomAddress string // For custom node connections
	CustomPort    uint
}

// FilterConfig defines the user's monitoring targets. The five lists
// are independent — the engine emits one rule per kind and OR-matches
// across them. MonitorEverything ignores the per-target lists and
// emits one coarse rule per event type. Persisted on TrayConfig (the
// sidecar engine config carries no per-target lists, since the
// cardano filter would AND-combine them on transaction events).
type FilterConfig struct {
	MonitorEverything bool     `yaml:"monitor_everything"`
	Wallets           []string `yaml:"wallets,omitempty"`
	DReps             []string `yaml:"dreps,omitempty"`
	Pools             []string `yaml:"pools,omitempty"`
	Assets            []string `yaml:"assets,omitempty"`
	Policies          []string `yaml:"policies,omitempty"`
}

// CloneFilter returns a deep copy of f with fresh slice backing
// arrays so mutations on one side don't leak to the other.
func CloneFilter(f FilterConfig) FilterConfig {
	out := f
	out.Wallets = append([]string(nil), f.Wallets...)
	out.DReps = append([]string(nil), f.DReps...)
	out.Pools = append([]string(nil), f.Pools...)
	out.Assets = append([]string(nil), f.Assets...)
	out.Policies = append([]string(nil), f.Policies...)
	return out
}

// ClonePlan returns a deep copy of p whose reference-typed fields
// (Filter slices, Notify map, Output.Config map) are independent, so a
// caller can mutate its copy without touching the source. Centralised
// here next to CloneFilter so adding a new reference-typed field to
// SetupPlan updates exactly one place.
func ClonePlan(p SetupPlan) SetupPlan {
	out := p
	out.Filter = CloneFilter(p.Filter)
	if p.Notify != nil {
		out.Notify = make(NotificationPrefs, len(p.Notify))
		maps.Copy(out.Notify, p.Notify)
	}
	if p.Output.Config != nil {
		out.Output.Config = make(map[string]string, len(p.Output.Config))
		maps.Copy(out.Output.Config, p.Output.Config)
	}
	return out
}

// OutputConfig defines the external event destination.
type OutputConfig struct {
	Type   string            // none, log, webhook, telegram
	Config map[string]string // Key-value pairs for plugin options
}

// APIConfig defines the local sidecar API settings.
type APIConfig struct {
	Address string
	Port    uint
}

// NotificationPrefs defines the user's desktop alert preferences.
type NotificationPrefs map[string]bool

// AppConfig defines tray-specific application settings.
type AppConfig struct {
	AutoStart bool
	// NotifyRateLimit / NotifyRateWindow override the engine's
	// notification rate limiter. Zero values resolve to
	// DefaultNotifyRateLimit / DefaultNotifyRateWindow at engine
	// construction time via TrayConfig.ResolvedNotifyRate.
	NotifyRateLimit  int
	NotifyRateWindow time.Duration
}

// Template names used by the wizard's three per-target sections. These
// strings are surfaced in cross-template hints (e.g. "looks like a
// Monitor Pool parameter — did you mean to pick \"Monitor Pool\"?") so
// they must match the wizard's section labels exactly.
const (
	templateWallet = "Watch Wallet"
	templateDRep   = "Track DRep"
	templatePool   = "Monitor Pool"
	templateAsset  = "Follow Asset"
	templatePolicy = "Follow Policy"
)

// SummarizeFilter returns a human-readable one-line description of a
// FilterConfig for use in dialogs, notifications, and the wizard's
// "Current configuration" line. Examples:
//
//	"everything"
//	"2 wallets, 1 DRep, 1 pool"
//	"2 wallets, 3 assets, 1 policy"
//	"nothing configured"
func SummarizeFilter(f FilterConfig) string {
	if f.MonitorEverything {
		return "everything"
	}
	var parts []string
	if n := len(f.Wallets); n > 0 {
		parts = append(parts, plural(n, "wallet", "wallets"))
	}
	if n := len(f.DReps); n > 0 {
		parts = append(parts, plural(n, "DRep", "DReps"))
	}
	if n := len(f.Pools); n > 0 {
		parts = append(parts, plural(n, "pool", "pools"))
	}
	if n := len(f.Assets); n > 0 {
		parts = append(parts, plural(n, "asset", "assets"))
	}
	if n := len(f.Policies); n > 0 {
		parts = append(parts, plural(n, "policy", "policies"))
	}
	if len(parts) == 0 {
		return "nothing configured"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// errEmptyParam is returned by each validator when called with an empty
// string. hex.DecodeString("") returns nil, so without this guard the
// DRep and Pool validators would silently accept "" as valid hex.
var errEmptyParam = errors.New("parameter must not be empty")

// ValidateWalletAddr checks that p is a Cardano payment or stake
// address (Phase 1: prefix check only — bech32 checksum validation is a
// follow-up). Surfaces a cross-template hint when the input's HRP
// matches a different template.
func ValidateWalletAddr(p string) error {
	if p == "" {
		return errEmptyParam
	}
	if strings.HasPrefix(p, "addr") ||
		strings.HasPrefix(p, "stake") {
		return nil
	}
	if hint := wrongTemplateHint(p, templateWallet); hint != "" {
		return errors.New(hint)
	}
	return fmt.Errorf(
		"invalid address: %s (must start with 'addr' or 'stake')",
		p,
	)
}

// ValidateDRepID checks that p is a DRep ID (drep1-prefixed bech32 or
// hex bytes). Surfaces a cross-template hint when the input's HRP
// matches a different template.
func ValidateDRepID(p string) error {
	if p == "" {
		return errEmptyParam
	}
	if strings.HasPrefix(p, "drep1") {
		return nil
	}
	if _, err := hex.DecodeString(p); err == nil {
		return nil
	}
	if hint := wrongTemplateHint(p, templateDRep); hint != "" {
		return errors.New(hint)
	}
	return fmt.Errorf(
		"invalid DRep ID: %s "+
			"(must start with 'drep1' or be hex bytes)",
		p,
	)
}

// ValidatePoolID checks that p is a stake-pool ID (pool1-prefixed
// bech32 or hex bytes). Surfaces a cross-template hint when the input's
// HRP matches a different template.
func ValidatePoolID(p string) error {
	if p == "" {
		return errEmptyParam
	}
	if strings.HasPrefix(p, "pool1") {
		return nil
	}
	if _, err := hex.DecodeString(p); err == nil {
		return nil
	}
	if hint := wrongTemplateHint(p, templatePool); hint != "" {
		return errors.New(hint)
	}
	return fmt.Errorf(
		"invalid Pool ID: %s "+
			"(must start with 'pool1' or be hex bytes)",
		p,
	)
}

// ValidateAssetFingerprint checks that p is a CIP-14 asset fingerprint
// (asset1-prefixed bech32). Hex is NOT accepted here because the
// natural hex form of an asset is `<policy>.<assetName>`, not a flat
// hex string — accepting flat hex would silently let policy IDs pass.
func ValidateAssetFingerprint(p string) error {
	if p == "" {
		return errEmptyParam
	}
	if strings.HasPrefix(p, "asset1") {
		return nil
	}
	if hint := wrongTemplateHint(p, templateAsset); hint != "" {
		return errors.New(hint)
	}
	return fmt.Errorf(
		"invalid asset fingerprint: %s "+
			"(must start with 'asset1' — CIP-14 bech32)",
		p,
	)
}

// ValidatePolicyID checks that p is a 56-character hex string (28-byte
// minting policy script hash). Length matters: a shorter or longer
// value is almost certainly the wrong field, so we reject it visibly
// rather than letting it flow into a never-matching rule.
func ValidatePolicyID(p string) error {
	if p == "" {
		return errEmptyParam
	}
	const policyHexLen = 56 // 28 bytes
	if len(p) == policyHexLen {
		if _, err := hex.DecodeString(p); err == nil {
			return nil
		}
	}
	if hint := wrongTemplateHint(p, templatePolicy); hint != "" {
		return errors.New(hint)
	}
	return fmt.Errorf(
		"invalid policy ID: %s "+
			"(must be a 56-character hex string)",
		p,
	)
}

// wrongTemplateHint returns a user-facing message when p's bech32 HRP
// matches a different template than the one the user selected, so the
// wizard can suggest switching sections rather than just rejecting the
// input as malformed. Returns "" when no other template's HRP matches,
// letting callers fall back to a generic format error.
func wrongTemplateHint(p, selected string) string {
	type hrp struct{ prefix, template string }
	// HRPs are disjoint so prefix order does not matter here.
	candidates := []hrp{
		{"drep1", templateDRep},
		{"pool1", templatePool},
		{"asset1", templateAsset},
		{"addr", templateWallet},
		{"stake", templateWallet},
	}
	for _, c := range candidates {
		if c.template == selected {
			continue
		}
		if strings.HasPrefix(p, c.prefix) {
			return fmt.Sprintf(
				"%q looks like a %s parameter — "+
					"did you mean to pick %q?",
				p, c.template, c.template,
			)
		}
	}
	return ""
}
