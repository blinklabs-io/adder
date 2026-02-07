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
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const configFileName = "adder-tray.yaml"

// TrayConfig holds the configuration for the adder-tray application.
type TrayConfig struct {
	// AdderBinary is the path to the adder binary.
	AdderBinary string `yaml:"adder_binary"`
	// AdderConfig is the path to the adder configuration file.
	AdderConfig string `yaml:"adder_config"`
	// AutoStart controls whether adder starts automatically
	// with the tray application.
	AutoStart bool `yaml:"auto_start"`
}

// DefaultConfig returns a TrayConfig with sensible defaults.
func DefaultConfig() TrayConfig {
	return TrayConfig{
		AdderBinary: "adder",
		AdderConfig: "",
		AutoStart:   false,
	}
}

// ConfigPath returns the full path to the tray configuration file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), configFileName)
}

// ConfigExists reports whether the configuration file exists on disk.
func ConfigExists() bool {
	_, err := os.Stat(ConfigPath())
	return err == nil
}

// LoadConfig reads the tray configuration from disk. If the file does
// not exist, it returns the default configuration.
func LoadConfig() (TrayConfig, error) {
	cfg := DefaultConfig()
	path := ConfigPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config: %w", err)
	}

	return cfg, nil
}

// SaveConfig writes the tray configuration to disk, creating the
// config directory if necessary.
func SaveConfig(cfg TrayConfig) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	if err := os.WriteFile(ConfigPath(), data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}
