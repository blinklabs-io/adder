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

package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MonitorTemplate represents a pre-configured monitoring template.
type MonitorTemplate int

const (
	WatchWallet MonitorTemplate = iota
	TrackDRep
	MonitorPool
)

// String returns the display name of the template.
func (t MonitorTemplate) String() string {
	switch t {
	case WatchWallet:
		return "Watch Wallet"
	case TrackDRep:
		return "Track DRep"
	case MonitorPool:
		return "Monitor Pool"
	default:
		return "Unknown"
	}
}

// AdderConfigParams holds the parameters for generating an adder
// configuration.
type AdderConfigParams struct {
	Network  string
	Template MonitorTemplate
	Param    string // The address/drep/pool value depending on Template
	Output   string // Output plugin name (default: "log")
	Format   string // Output format (default: "json")
}

// adderConfig is the internal structure matching adder's config.yaml
// format. We use map types for the plugins section to match adder's
// flexible config.
type adderConfig struct {
	Input   string                            `yaml:"input"`
	Output  string                            `yaml:"output"`
	API     adderAPIConfig                    `yaml:"api"`
	Logging adderLoggingConfig                `yaml:"logging"`
	Plugins map[string]map[string]interface{} `yaml:"plugins"`
}

type adderAPIConfig struct {
	Address string `yaml:"address"`
	Port    uint   `yaml:"port"`
}

type adderLoggingConfig struct {
	Level string `yaml:"level"`
}

// GenerateAdderConfig builds an adder configuration from the given
// parameters. The API server is always enabled because adder-tray
// connects to its /events endpoint for desktop notifications and
// /healthcheck for status monitoring.
func GenerateAdderConfig(params AdderConfigParams) ([]byte, error) {
	if params.Network == "" {
		return nil, errors.New("network is required")
	}
	if params.Param == "" {
		return nil, errors.New("filter parameter is required")
	}

	output := params.Output
	if output == "" {
		output = "log"
	}
	format := params.Format
	if format == "" {
		format = "json"
	}

	cfg := adderConfig{
		Input:  "chainsync",
		Output: output,
		API: adderAPIConfig{
			Address: "127.0.0.1",
			Port:    8080,
		},
		Logging: adderLoggingConfig{
			Level: "info",
		},
		Plugins: map[string]map[string]interface{}{
			"input": {
				"chainsync": map[string]interface{}{
					"network": params.Network,
				},
			},
			"output": {
				output: map[string]interface{}{
					"format": format,
				},
			},
		},
	}

	// Add filter config based on template
	filterKey := templateFilterKey(params.Template)
	if filterKey == "" {
		return nil, fmt.Errorf("unsupported monitor template: %d", params.Template)
	}
	cfg.Plugins["filter"] = map[string]interface{}{
		"chainsync": map[string]interface{}{
			filterKey: params.Param,
		},
	}

	return yaml.Marshal(&cfg)
}

// templateFilterKey returns the filter configuration key for a
// template. Returns an empty string for unrecognized templates.
func templateFilterKey(t MonitorTemplate) string {
	switch t {
	case WatchWallet:
		return "address"
	case TrackDRep:
		return "drep"
	case MonitorPool:
		return "pool"
	default:
		return ""
	}
}

// AdderConfigPath returns the path to the adder config file in the
// config directory.
func AdderConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

// WriteAdderConfig writes the adder configuration to the config
// directory.
func WriteAdderConfig(params AdderConfigParams) error {
	data, err := GenerateAdderConfig(params)
	if err != nil {
		return fmt.Errorf("generating config: %w", err)
	}

	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("setting config directory permissions: %w", err)
	}

	if err := os.WriteFile(AdderConfigPath(), data, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}
