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

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func writeNotificationConfig(t *testing.T, cfg setup.NotificationConfig) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "notifications.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func runNotificationValidation(
	t *testing.T,
	path string,
) (notificationValidationResult, error) {
	t.Helper()
	cmd := newNotificationsCmd()
	cmd.SilenceUsage = true
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"validate", "--config", path, "--json"})
	err := cmd.Execute()
	var result notificationValidationResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	return result, err
}

func TestNotificationsValidateJSON(t *testing.T) {
	cfg := setup.DefaultNotificationConfig()
	cfg.Monitor.Everything = true
	result, err := runNotificationValidation(t, writeNotificationConfig(t, cfg))
	require.NoError(t, err)
	require.True(t, result.Valid)
	require.Empty(t, result.Errors)
}

func TestNotificationsValidateJSONReportsIssues(t *testing.T) {
	cfg := setup.DefaultNotificationConfig()
	cfg.SchemaVersion = 2
	result, err := runNotificationValidation(t, writeNotificationConfig(t, cfg))
	require.ErrorContains(t, err, "configuration is invalid")
	require.False(t, result.Valid)
	require.NotEmpty(t, result.Errors)
	require.Equal(t, "schemaVersion", result.Errors[0].Field)
}

func TestValidateNotificationInput(t *testing.T) {
	for _, test := range []struct {
		name          string
		inputNetwork  string
		inputAddress  string
		customAddress string
		customPort    uint
		expectedError string
	}{
		{
			name:         "preview",
			inputNetwork: "preview",
		},
		{
			name:          "network mismatch",
			inputNetwork:  "mainnet",
			expectedError: "does not match chainsync network",
		},
		{
			name:          "custom node mismatch",
			inputNetwork:  "preview",
			inputAddress:  "other.example:3001",
			customAddress: "node.example",
			customPort:    3001,
			expectedError: "does not match chainsync address",
		},
		{
			name:          "custom node",
			inputNetwork:  "preview",
			inputAddress:  "node.example:3001",
			customAddress: "node.example",
			customPort:    3001,
		},
		{
			name:          "IPv6 custom node",
			inputNetwork:  "preview",
			inputAddress:  "[::1]:3001",
			customAddress: "::1",
			customPort:    3001,
		},
		{
			name:          "equivalent IPv6 custom node",
			inputNetwork:  "preview",
			inputAddress:  "[0:0:0:0:0:0:0:1]:3001",
			customAddress: "::1",
			customPort:    3001,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := setup.DefaultNotificationConfig()
			cfg.Network.Name = "preview"
			cfg.Network.CustomAddress = test.customAddress
			cfg.Network.CustomPort = test.customPort
			cfg.Monitor.Everything = true

			resolved, err := plugin.ResolveConfig(map[string]map[string]map[string]any{
				"input":  {"chainsync": {"network": test.inputNetwork, "address": test.inputAddress}},
				"output": {"notify-json": {"config": writeNotificationConfig(t, cfg)}},
			}, nil, func(string) (string, bool) { return "", false })
			require.NoError(t, err)
			err = validateNotificationInput(resolved, "chainsync", "notify-json")
			if test.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.expectedError)
			}
		})
	}
}

func TestNotificationValidationUsesResolvedSources(t *testing.T) {
	cfg := setup.DefaultNotificationConfig()
	cfg.Network.Name = "preview"
	cfg.Network.CustomAddress = "node.example"
	cfg.Network.CustomPort = 3001
	cfg.Monitor.Everything = true
	path := writeNotificationConfig(t, cfg)
	for _, test := range []struct {
		name      string
		env       map[string]string
		flags     []string
		wantError string
	}{
		{name: "YAML only"},
		{
			name: "environment overrides YAML",
			env: map[string]string{
				"OUTPUT_NOTIFY_JSON_CONFIG": path,
				"INPUT_CHAINSYNC_NETWORK":   "preview",
				"INPUT_CHAINSYNC_ADDRESS":   "node.example:3001",
			},
		},
		{
			name: "CLI overrides environment and YAML",
			env: map[string]string{
				"OUTPUT_NOTIFY_JSON_CONFIG": "unused.json",
				"CARDANO_NETWORK":           "mainnet",
				"INPUT_CHAINSYNC_ADDRESS":   "other.example:3001",
			},
			flags: []string{
				"--output-notify-json-config=" + path,
				"--input-chainsync-network=preview",
				"--input-chainsync-address=node.example:3001",
			},
		},
		{
			name:      "resolved network mismatch",
			env:       map[string]string{"CARDANO_NETWORK": "mainnet"},
			wantError: "does not match chainsync network",
		},
		{
			name:      "resolved address mismatch",
			env:       map[string]string{"INPUT_CHAINSYNC_ADDRESS": "other.example:3001"},
			wantError: "does not match chainsync address",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := map[string]map[string]map[string]any{
				"input":  {"chainsync": {"network": "preview", "address": "node.example:3001"}},
				"output": {"notify-json": {"config": path}},
			}
			if test.name == "environment overrides YAML" {
				data["input"]["chainsync"]["network"] = "mainnet"
				data["input"]["chainsync"]["address"] = "other.example:3001"
				data["output"]["notify-json"]["config"] = "unused.json"
			}
			fs := pflag.NewFlagSet(test.name, pflag.ContinueOnError)
			require.NoError(t, plugin.PopulateCmdlineOptions(fs))
			require.NoError(t, fs.Parse(test.flags))
			resolved, err := plugin.ResolveConfig(data, fs, func(key string) (string, bool) {
				value, ok := test.env[key]
				return value, ok
			})
			require.NoError(t, err)
			err = validateNotificationInput(resolved, "chainsync", "notify-json")
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			_, err = resolved.New(plugin.PluginTypeOutput, "notify-json")
			require.NoError(t, err)
			_, err = resolved.New(plugin.PluginTypeInput, "chainsync")
			require.NoError(t, err)
		})
	}
}
