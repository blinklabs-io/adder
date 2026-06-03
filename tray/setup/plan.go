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
	"strings"
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
	// NotifyPrefPoolParams is the preference key for pool parameter change alerts.
	NotifyPrefPoolParams = "Pool parameter changes"
	// NotifyPrefConnectionIssues is the preference key for connection status
	// alerts.
	NotifyPrefConnectionIssues = "Connection issues"
)

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

// FilterConfig defines what the user wants to monitor. The three lists
// (Wallets, DReps, Pools) can be populated independently; the engine
// emits one rule per entry across all of them, so the user can watch a
// wallet AND track a DRep AND monitor a pool at the same time. When
// MonitorEverything is true the per-target lists are ignored and the
// engine emits a single coarse rule per event type instead.
type FilterConfig struct {
	MonitorEverything bool
	Wallets           []string // addr1.../stake1...
	DReps             []string // drep1... or hex
	Pools             []string // pool1... or hex
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
}

// Template names used by the wizard's three per-target sections. These
// strings are surfaced in cross-template hints (e.g. "looks like a
// Monitor Pool parameter — did you mean to pick \"Monitor Pool\"?") so
// they must match the wizard's section labels exactly.
const (
	templateWallet = "Watch Wallet"
	templateDRep   = "Track DRep"
	templatePool   = "Monitor Pool"
)

// SummarizeFilter returns a human-readable one-line description of a
// FilterConfig for use in dialogs, notifications, and the wizard's
// "Current configuration" line. Examples:
//
//	"everything"
//	"2 wallets, 1 DRep, 1 pool"
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
