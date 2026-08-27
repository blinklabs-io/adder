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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func validNotificationConfig() NotificationConfig {
	cfg := DefaultNotificationConfig()
	cfg.Monitor.Wallets = []string{"addr1test"}
	return cfg
}

func TestNotificationConfigRoundTrip(t *testing.T) {
	cfg, err := DecodeNotificationConfig(strings.NewReader(`{
  "schemaVersion": 1,
  "network": {"name": "preview"},
  "monitor": {
    "everything": false,
    "wallets": ["addr_test1example"],
    "drepMatch": "any",
    "poolMatch": "any",
    "assetMatch": "any",
    "policyMatch": "any"
  },
  "alerts": {"incomingTransactions": true},
  "rateLimit": {"max": 2, "windowSeconds": 10},
  "connectionStaleSeconds": 90
}`))
	require.NoError(t, err)
	require.Equal(t, "preview", cfg.Network.Name)
	require.Equal(t, []string{"addr_test1example"}, cfg.Monitor.Wallets)
	plan := cfg.SetupPlan()
	require.True(t, plan.Notify[NotifyPrefIncomingTx])
	require.Equal(t, 2, plan.App.NotifyRateLimit)
}

func TestNotificationConfigNormalizesTextValues(t *testing.T) {
	cfg, err := DecodeNotificationConfig(strings.NewReader(`{
  "schemaVersion": 1,
  "network": {"name":" mainnet ","customAddress":" node.example ","customPort":3001},
  "monitor": {
    "wallets":[" addr1test "],"dreps":[],"pools":[],"assets":[],"policies":[],
    "drepMatch":" any ","poolMatch":"","assetMatch":"","policyMatch":""
  },
  "alerts": {"incomingTransactions":true},
  "rateLimit": {"max":1,"windowSeconds":5},
  "connectionStaleSeconds":90
}`))
	require.NoError(t, err)
	require.Equal(t, "mainnet", cfg.Network.Name)
	require.Equal(t, "node.example", cfg.Network.CustomAddress)
	require.Equal(t, []string{"addr1test"}, cfg.Monitor.Wallets)
	require.Equal(t, AdvancedMatchAny, cfg.Monitor.DRepMatch)

	plan := cfg.SetupPlan()
	require.Equal(t, "node.example", plan.Network.CustomAddress)
	require.Equal(t, []string{"addr1test"}, plan.Filter.Wallets)
	require.Equal(t, "mainnet", cfg.NetworkLabel())
}

func TestNotificationConfigRejectsUnknownFields(t *testing.T) {
	_, err := DecodeNotificationConfig(strings.NewReader(`{"schemaVersion":1,"surprise":true}`))
	require.ErrorContains(t, err, "unknown field")
}

func TestNotificationConfigValidationIssues(t *testing.T) {
	cfg := validNotificationConfig()
	cfg.SchemaVersion = 2
	cfg.Network.Name = "custom"
	cfg.Monitor.Wallets = append(cfg.Monitor.Wallets, cfg.Monitor.Wallets[0])
	cfg.RateLimit.WindowSeconds = 0
	cfg.ConnectionStaleSeconds = 10
	issues := cfg.ValidationIssues()
	require.NotEmpty(t, issues)
	fields := make(map[string]bool)
	for _, issue := range issues {
		fields[issue.Field] = true
	}
	require.True(t, fields["schemaVersion"])
	require.True(t, fields["network.name"])
	require.True(t, fields["monitor.wallets[1]"])
	require.True(t, fields["rateLimit.windowSeconds"])
	require.True(t, fields["connectionStaleSeconds"])
}

func TestNotificationConfigRequiresExplicitScope(t *testing.T) {
	cfg := DefaultNotificationConfig()
	_, err := DecodeNotificationConfig(strings.NewReader(mustJSON(t, cfg)))
	var validationErr ValidationIssuesError
	require.True(t, errors.As(err, &validationErr))
	require.Contains(t, validationErr.Error(), "configure at least one target")

	cfg.Monitor.Everything = true
	_, err = DecodeNotificationConfig(strings.NewReader(mustJSON(t, cfg)))
	require.NoError(t, err)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
