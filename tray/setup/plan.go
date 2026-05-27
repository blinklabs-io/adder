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

// FilterConfig defines the monitoring template and its parameters.
type FilterConfig struct {
	Template string // Watch Wallet, Track DRep, Monitor Pool, Monitor Everything
	Param    string // Comma-separated list of IDs
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

// ValidateTemplateParam checks if the given parameter (or list of parameters)
// is valid for the selected template.
func ValidateTemplateParam(template, param string) error {
	if template == "Monitor Everything" {
		return nil
	}
	if param == "" {
		return errors.New("parameter is required")
	}

	parts := strings.Split(param, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return errors.New("invalid parameter format: empty entry found in list")
		}
		if err := validateSingleParam(template, p); err != nil {
			return err
		}
	}
	return nil
}

func validateSingleParam(template, p string) error {
	switch template {
	case "Watch Wallet":
		if !strings.HasPrefix(p, "addr") &&
			!strings.HasPrefix(p, "stake") {
			return fmt.Errorf("invalid address: %s (must start with 'addr' or 'stake')", p)
		}
	case "Track DRep":
		if !strings.HasPrefix(p, "drep1") {
			if _, err := hex.DecodeString(p); err != nil {
				return fmt.Errorf("invalid DRep ID: %s (must be bech32 or hex)", p)
			}
		}
	case "Monitor Pool":
		if !strings.HasPrefix(p, "pool1") {
			if _, err := hex.DecodeString(p); err != nil {
				return fmt.Errorf("invalid Pool ID: %s (must be bech32 or hex)", p)
			}
		}
	}
	return nil
}
