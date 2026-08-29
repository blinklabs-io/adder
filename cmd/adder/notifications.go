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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/spf13/cobra"
)

type notificationValidationResult struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Valid         bool                    `json:"valid"`
	Errors        []setup.ValidationIssue `json:"errors,omitempty"`
}

func normalizeHostPort(address string) (string, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, strconv.FormatUint(port, 10)), nil
}

func validateNotificationInput(
	cmd *cobra.Command,
	inputName string,
	outputName string,
) error {
	if outputName != "notify-json" {
		return nil
	}
	if inputName != "chainsync" {
		return fmt.Errorf("notify-json requires chainsync input, got %q", inputName)
	}
	configPath, err := cmd.Flags().GetString("output-notify-json-config")
	if err != nil {
		return err
	}
	if configPath == "" {
		return errors.New("notify-json config path must not be empty")
	}
	cfg, err := setup.ReadNotificationConfig(configPath)
	if err != nil {
		return err
	}
	inputNetwork, err := cmd.Flags().GetString("input-chainsync-network")
	if err != nil {
		return err
	}
	if inputNetwork != cfg.Network.Name {
		return fmt.Errorf(
			"notification network %q does not match chainsync network %q",
			cfg.Network.Name,
			inputNetwork,
		)
	}
	if cfg.Network.CustomAddress == "" {
		return nil
	}
	inputAddress, err := cmd.Flags().GetString("input-chainsync-address")
	if err != nil {
		return err
	}
	expectedAddress := net.JoinHostPort(
		cfg.Network.CustomAddress,
		strconv.FormatUint(uint64(cfg.Network.CustomPort), 10),
	)
	normalizedExpected, err := normalizeHostPort(expectedAddress)
	if err != nil {
		return fmt.Errorf("invalid notification custom node %q: %w", expectedAddress, err)
	}
	normalizedInput, err := normalizeHostPort(inputAddress)
	if err != nil {
		return fmt.Errorf("invalid chainsync address %q: %w", inputAddress, err)
	}
	if normalizedInput != normalizedExpected {
		return fmt.Errorf(
			"notification custom node %q does not match chainsync address %q",
			expectedAddress,
			inputAddress,
		)
	}
	return nil
}

func newNotificationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notifications",
		Short: "Manage target-aware notification configuration",
	}
	var path string
	var jsonOutput bool
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate a notification JSON configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return errors.New("--config is required")
			}
			_, err := setup.ReadNotificationConfig(path)
			result := notificationValidationResult{
				SchemaVersion: setup.NotificationConfigSchemaVersion,
				Valid:         err == nil,
			}
			if err != nil {
				var validationErr setup.ValidationIssuesError
				if errors.As(err, &validationErr) {
					result.Errors = validationErr.Issues
				} else {
					result.Errors = []setup.ValidationIssue{{
						Field: "config", Message: err.Error(),
					}}
				}
			}
			if jsonOutput {
				if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
					return fmt.Errorf("encoding validation result: %w", encodeErr)
				}
			} else if err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "notification configuration is valid")
			} else {
				for _, issue := range result.Errors {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", issue.Field, issue.Message)
				}
			}
			if err != nil {
				return errors.New("notification configuration is invalid")
			}
			return nil
		},
	}
	validate.Flags().StringVar(&path, "config", "", "path to notification JSON configuration")
	validate.Flags().BoolVar(&jsonOutput, "json", false, "emit a machine-readable validation result")
	cmd.AddCommand(validate)
	return cmd
}
