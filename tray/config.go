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
	"log/slog"
	"os"
	"path/filepath"

	"github.com/blinklabs-io/adder/tray/setup"
	"gopkg.in/yaml.v3"
)

const configFileName = "adder-tray.yaml"

// TrayConfig is an alias for setup.TrayConfig to maintain backward
// compatibility.
type TrayConfig = setup.TrayConfig

// DefaultConfig returns a TrayConfig with sensible defaults.
func DefaultConfig() TrayConfig {
	return TrayConfig{
		APIAddress:  "127.0.0.1",
		APIPort:     8080,
		AdderConfig: "",
		AutoStart:   false,
		NotifyPrefs: make(map[string]bool),
	}
}

// ConfigDir returns the platform-specific directory for storing
// configuration files. It can be overridden by the ADDER_TRAY_CONFIG_DIR
// environment variable.
func ConfigDir() string {
	return setup.ConfigDir()
}

// LogDir returns the platform-specific directory for storing log files.
// It can be overridden by the ADDER_TRAY_LOG_DIR environment variable.
func LogDir() string {
	return setup.LogDir()
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
			slog.Info("config file not found, using defaults", "path", path)
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config: %w", err)
	}

	if err := validateConfig(cfg); err != nil {
		slog.Error("config validation failed", "error", err)
		return cfg, err
	}

	slog.Info("successfully loaded config", "path", path)
	return cfg, nil
}

// validateConfig checks that required TrayConfig fields are set.
func validateConfig(cfg TrayConfig) error {
	if cfg.APIAddress == "" {
		return errors.New("invalid config: api_address must not be empty")
	}
	if cfg.APIPort == 0 || cfg.APIPort > 65535 {
		return errors.New(
			"invalid config: api_port must be between 1 and 65535",
		)
	}
	return nil
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
