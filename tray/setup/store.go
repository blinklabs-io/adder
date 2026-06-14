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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blinklabs-io/adder/internal/config"
	"gopkg.in/yaml.v3"
)

// TrayConfig holds the configuration for the adder-tray application.
// Filter is the authoritative source of monitoring targets — the
// sidecar engine config carries no per-target lists, because the
// cardano filter would AND-combine them on transaction events.
type TrayConfig struct {
	APIAddress  string          `yaml:"api_address"`
	APIPort     uint            `yaml:"api_port"`
	AdderConfig string          `yaml:"adder_config"`
	AutoStart   bool            `yaml:"auto_start"`
	NotifyPrefs map[string]bool `yaml:"notify_prefs"`
	Filter      FilterConfig    `yaml:"filter,omitempty"`

	// NotifyRateLimit caps emitted notifications per
	// NotifyRateWindow. Matches beyond the cap are coalesced into a
	// single "Multiple events occurred" batch fired at the window
	// boundary. A non-positive value disables the limiter (every
	// match fires immediately). Zero (the YAML default for omitted
	// fields) is resolved to DefaultNotifyRateLimit at load time.
	NotifyRateLimit int `yaml:"notify_rate_limit,omitempty"`
	// NotifyRateWindow is the limiter window. Zero is resolved to
	// DefaultNotifyRateWindow at load time.
	NotifyRateWindow time.Duration `yaml:"notify_rate_window,omitempty"`
}

// Default values for the notification rate limiter. Used when the
// YAML config omits the fields or leaves them at zero.
const (
	DefaultNotifyRateLimit  = 1
	DefaultNotifyRateWindow = 5 * time.Second
)

// ResolvedNotifyRate returns the rate-limit + window the engine
// should use, substituting defaults for zero values. A negative
// NotifyRateLimit is passed through (disables the limiter) so power
// users can opt out of coalescing entirely.
func (c TrayConfig) ResolvedNotifyRate() (int, time.Duration) {
	limit := c.NotifyRateLimit
	if limit == 0 {
		limit = DefaultNotifyRateLimit
	}
	window := c.NotifyRateWindow
	if window <= 0 {
		window = DefaultNotifyRateWindow
	}
	return limit, window
}

// ConfigStore defines the interface for persisting engine and tray configurations.
type ConfigStore interface {
	LoadEngine(path string) (config.Config, error)
	SaveEngineAtomic(path string, cfg config.Config) error
	LoadTray() (TrayConfig, error)
	SaveTrayAtomic(cfg TrayConfig) error
}

// LocalStore implements ConfigStore using the local filesystem.
type LocalStore struct {
	TrayConfigPath string
}

func (s *LocalStore) LoadEngine(path string) (config.Config, error) {
	cfg := *config.GetConfig()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	// cfg is a shallow copy of the global singleton, so its reference-typed
	// fields still alias the global's. Detach them before Load() so that
	// unmarshalling the engine file (which writes through an existing map /
	// pointer) repopulates them on the copy instead of mutating global state.
	// Load()/populate*() restore the same defaults for these fields.
	// NOTE: extend this list if Config gains other map/slice/pointer fields.
	cfg.Plugin = nil
	cfg.ByronGenesis.EndSlot = nil
	if err := cfg.Load(path); err != nil {
		return cfg, fmt.Errorf("loading engine config: %w", err)
	}
	return cfg, nil
}

func (s *LocalStore) SaveEngineAtomic(path string, cfg config.Config) error {
	return config.SaveAtomic(path, &cfg)
}

func (s *LocalStore) LoadTray() (TrayConfig, error) {
	cfg := TrayConfig{
		APIAddress: "127.0.0.1",
		APIPort:    8080,
	}
	if _, err := os.Stat(s.TrayConfigPath); os.IsNotExist(err) {
		return cfg, nil
	}
	data, err := os.ReadFile(s.TrayConfigPath)
	if err != nil {
		return cfg, fmt.Errorf("reading tray config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing tray config: %w", err)
	}
	return cfg, nil
}

func (s *LocalStore) SaveTrayAtomic(cfg TrayConfig) error {
	dir := filepath.Dir(s.TrayConfigPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	return config.SaveAtomic(s.TrayConfigPath, &cfg)
}
