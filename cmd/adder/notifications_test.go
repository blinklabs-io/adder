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

	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/spf13/cobra"
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

			cmd := &cobra.Command{}
			cmd.Flags().String(
				"output-notify-json-config",
				writeNotificationConfig(t, cfg),
				"",
			)
			cmd.Flags().String("input-chainsync-network", test.inputNetwork, "")
			cmd.Flags().String("input-chainsync-address", test.inputAddress, "")
			err := validateNotificationInput(cmd, "chainsync", "notify-json")
			if test.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.expectedError)
			}
		})
	}
}
