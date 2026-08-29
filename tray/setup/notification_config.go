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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	NotificationConfigSchemaVersion = 1
	DefaultConnectionStaleSeconds   = 90

	AlertIncomingTransactions = "incomingTransactions"
	AlertOutgoingTransactions = "outgoingTransactions"
	AlertTokenTransfers       = "tokenTransfers"
	AlertBlocksMinted         = "blocksMinted"
	AlertChainRollbacks       = "chainRollbacks"
	AlertPoolParameterChanges = "poolParameterChanges"
	AlertGovernanceProposals  = "governanceProposals"
	AlertVotesCast            = "votesCast"
	AlertRegistrationChanges  = "registrationChanges"
	AlertAssetActivity        = "assetActivity"
	AlertPolicyActivity       = "policyActivity"
	AlertConnectionIssues     = "connectionIssues"
)

// NotificationConfig is the UI-neutral configuration consumed by headless
// notification outputs. It deliberately uses stable machine keys rather than
// the tray's user-facing labels so non-Go clients can safely persist it.
type NotificationConfig struct {
	SchemaVersion          int                       `json:"schemaVersion"`
	Network                NotificationNetworkConfig `json:"network"`
	Monitor                NotificationMonitorConfig `json:"monitor"`
	Alerts                 map[string]bool           `json:"alerts"`
	RateLimit              NotificationRateConfig    `json:"rateLimit"`
	ConnectionStaleSeconds int                       `json:"connectionStaleSeconds"`
}

type NotificationNetworkConfig struct {
	Name          string `json:"name"`
	CustomAddress string `json:"customAddress,omitempty"`
	CustomPort    uint   `json:"customPort,omitempty"`
}

type NotificationMonitorConfig struct {
	Everything  bool              `json:"everything"`
	Wallets     []string          `json:"wallets,omitempty"`
	DReps       []string          `json:"dreps,omitempty"`
	Pools       []string          `json:"pools,omitempty"`
	Assets      []string          `json:"assets,omitempty"`
	Policies    []string          `json:"policies,omitempty"`
	DRepMatch   AdvancedMatchMode `json:"drepMatch,omitempty"`
	PoolMatch   AdvancedMatchMode `json:"poolMatch,omitempty"`
	AssetMatch  AdvancedMatchMode `json:"assetMatch,omitempty"`
	PolicyMatch AdvancedMatchMode `json:"policyMatch,omitempty"`
}

type NotificationRateConfig struct {
	Max           int `json:"max"`
	WindowSeconds int `json:"windowSeconds"`
}

// ValidationIssue is stable JSON output for configuration frontends.
type ValidationIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func DefaultNotificationConfig() NotificationConfig {
	return NotificationConfig{
		SchemaVersion: NotificationConfigSchemaVersion,
		Network: NotificationNetworkConfig{
			Name: "mainnet",
		},
		Monitor: NotificationMonitorConfig{},
		Alerts: map[string]bool{
			AlertIncomingTransactions: true,
			AlertOutgoingTransactions: true,
			AlertTokenTransfers:       true,
			AlertBlocksMinted:         true,
			AlertChainRollbacks:       true,
			AlertPoolParameterChanges: true,
			AlertGovernanceProposals:  true,
			AlertVotesCast:            true,
			AlertRegistrationChanges:  true,
			AlertAssetActivity:        true,
			AlertPolicyActivity:       true,
			AlertConnectionIssues:     true,
		},
		RateLimit: NotificationRateConfig{
			Max:           DefaultNotifyRateLimit,
			WindowSeconds: int(DefaultNotifyRateWindow / time.Second),
		},
		ConnectionStaleSeconds: DefaultConnectionStaleSeconds,
	}
}

func ReadNotificationConfig(path string) (NotificationConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return NotificationConfig{}, fmt.Errorf("reading notification config: %w", err)
	}
	defer f.Close()
	return DecodeNotificationConfig(f)
}

func DecodeNotificationConfig(r io.Reader) (NotificationConfig, error) {
	var cfg NotificationConfig
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parsing notification config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return cfg, fmt.Errorf("parsing notification config: %w", err)
	}
	normalizeNotificationConfig(&cfg)
	if issues := cfg.ValidationIssues(); len(issues) > 0 {
		return cfg, ValidationIssuesError{Issues: issues}
	}
	return cfg, nil
}

func normalizeNotificationConfig(c *NotificationConfig) {
	c.Network.Name = strings.TrimSpace(c.Network.Name)
	c.Network.CustomAddress = strings.TrimSpace(c.Network.CustomAddress)
	c.Monitor.DRepMatch = AdvancedMatchMode(strings.TrimSpace(string(c.Monitor.DRepMatch)))
	c.Monitor.PoolMatch = AdvancedMatchMode(strings.TrimSpace(string(c.Monitor.PoolMatch)))
	c.Monitor.AssetMatch = AdvancedMatchMode(strings.TrimSpace(string(c.Monitor.AssetMatch)))
	c.Monitor.PolicyMatch = AdvancedMatchMode(strings.TrimSpace(string(c.Monitor.PolicyMatch)))
	trimAll := func(values []string) {
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
		}
	}
	trimAll(c.Monitor.Wallets)
	trimAll(c.Monitor.DReps)
	trimAll(c.Monitor.Pools)
	trimAll(c.Monitor.Assets)
	trimAll(c.Monitor.Policies)
}

type ValidationIssuesError struct {
	Issues []ValidationIssue
}

func (e ValidationIssuesError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Field+": "+issue.Message)
	}
	return strings.Join(parts, "; ")
}

func (c NotificationConfig) ValidationIssues() []ValidationIssue {
	var issues []ValidationIssue
	add := func(field, message string) {
		issues = append(issues, ValidationIssue{Field: field, Message: message})
	}

	if c.SchemaVersion != NotificationConfigSchemaVersion {
		add("schemaVersion", fmt.Sprintf("must be %d", NotificationConfigSchemaVersion))
	}
	switch c.Network.Name {
	case "mainnet", "preprod", "preview":
	default:
		add("network.name", "must be mainnet, preprod, or preview")
	}
	if strings.TrimSpace(c.Network.CustomAddress) == "" && c.Network.CustomPort != 0 {
		add("network.customAddress", "must be set when customPort is set")
	}
	if strings.TrimSpace(c.Network.CustomAddress) != "" &&
		(c.Network.CustomPort == 0 || c.Network.CustomPort > 65535) {
		add("network.customPort", "must be between 1 and 65535 when using a custom node")
	}

	validateTargets := func(field string, values []string, validate func(string) error) {
		seen := make(map[string]struct{}, len(values))
		for i, value := range values {
			value = strings.TrimSpace(value)
			itemField := fmt.Sprintf("monitor.%s[%d]", field, i)
			if err := validate(value); err != nil {
				add(itemField, err.Error())
			}
			key := strings.ToLower(value)
			if _, ok := seen[key]; ok {
				add(itemField, "duplicate target")
			}
			seen[key] = struct{}{}
		}
	}
	validateTargets("wallets", c.Monitor.Wallets, ValidateWalletAddr)
	validateTargets("dreps", c.Monitor.DReps, ValidateDRepID)
	validateTargets("pools", c.Monitor.Pools, ValidatePoolID)
	validateTargets("assets", c.Monitor.Assets, ValidateAssetFingerprint)
	validateTargets("policies", c.Monitor.Policies, ValidatePolicyID)

	if !c.Monitor.Everything && len(c.Monitor.Wallets)+len(c.Monitor.DReps)+
		len(c.Monitor.Pools)+len(c.Monitor.Assets)+len(c.Monitor.Policies) == 0 {
		add("monitor", "configure at least one target or enable everything")
	}
	validateMatch := func(field string, mode AdvancedMatchMode) {
		if mode != "" && mode != AdvancedMatchAny && mode != AdvancedMatchAll {
			add(field, "must be any or all")
		}
	}
	validateMatch("monitor.drepMatch", c.Monitor.DRepMatch)
	validateMatch("monitor.poolMatch", c.Monitor.PoolMatch)
	validateMatch("monitor.assetMatch", c.Monitor.AssetMatch)
	validateMatch("monitor.policyMatch", c.Monitor.PolicyMatch)

	knownAlerts := map[string]struct{}{
		AlertIncomingTransactions: {}, AlertOutgoingTransactions: {},
		AlertTokenTransfers: {}, AlertBlocksMinted: {}, AlertChainRollbacks: {},
		AlertPoolParameterChanges: {}, AlertGovernanceProposals: {},
		AlertVotesCast: {}, AlertRegistrationChanges: {}, AlertAssetActivity: {},
		AlertPolicyActivity: {}, AlertConnectionIssues: {},
	}
	anyAlert := false
	for key, enabled := range c.Alerts {
		if _, ok := knownAlerts[key]; !ok {
			add("alerts."+key, "unknown alert category")
		}
		anyAlert = anyAlert || enabled
	}
	if !anyAlert {
		add("alerts", "enable at least one alert category")
	}
	if c.RateLimit.Max < -1 {
		add("rateLimit.max", "must be -1 (disabled) or zero or greater")
	}
	if c.RateLimit.WindowSeconds <= 0 {
		add("rateLimit.windowSeconds", "must be greater than zero")
	}
	if c.ConnectionStaleSeconds < 30 || c.ConnectionStaleSeconds > 3600 {
		add("connectionStaleSeconds", "must be between 30 and 3600")
	}
	return issues
}

func (c NotificationConfig) SetupPlan() SetupPlan {
	prefs := NotificationPrefs{
		NotifyPrefIncomingTx:       c.Alerts[AlertIncomingTransactions],
		NotifyPrefOutgoingTx:       c.Alerts[AlertOutgoingTransactions],
		NotifyPrefTokenTransfers:   c.Alerts[AlertTokenTransfers],
		NotifyPrefBlocksMinted:     c.Alerts[AlertBlocksMinted],
		NotifyPrefChainRollbacks:   c.Alerts[AlertChainRollbacks],
		NotifyPrefPoolParams:       c.Alerts[AlertPoolParameterChanges],
		NotifyPrefGovProposals:     c.Alerts[AlertGovernanceProposals],
		NotifyPrefVotesCast:        c.Alerts[AlertVotesCast],
		NotifyPrefRegChanges:       c.Alerts[AlertRegistrationChanges],
		NotifyPrefAssetActivity:    c.Alerts[AlertAssetActivity],
		NotifyPrefPolicyActivity:   c.Alerts[AlertPolicyActivity],
		NotifyPrefConnectionIssues: c.Alerts[AlertConnectionIssues],
	}
	return SetupPlan{
		Network: NetworkConfig{
			Name:          c.Network.Name,
			CustomAddress: c.Network.CustomAddress,
			CustomPort:    c.Network.CustomPort,
		},
		Filter: FilterConfig{
			MonitorEverything: c.Monitor.Everything,
			Wallets:           append([]string(nil), c.Monitor.Wallets...),
			DReps:             append([]string(nil), c.Monitor.DReps...),
			Pools:             append([]string(nil), c.Monitor.Pools...),
			Assets:            append([]string(nil), c.Monitor.Assets...),
			Policies:          append([]string(nil), c.Monitor.Policies...),
			DRepMatch:         c.Monitor.DRepMatch,
			PoolMatch:         c.Monitor.PoolMatch,
			AssetMatch:        c.Monitor.AssetMatch,
			PolicyMatch:       c.Monitor.PolicyMatch,
		},
		Notify: prefs,
		App: AppConfig{
			NotifyRateLimit:  c.RateLimit.Max,
			NotifyRateWindow: time.Duration(c.RateLimit.WindowSeconds) * time.Second,
		},
	}
}

func (c NotificationConfig) NetworkLabel() string {
	return c.Network.Name
}
