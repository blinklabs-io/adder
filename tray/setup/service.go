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
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// ServiceManager defines the interface for idempotent service lifecycle
// management.
type ServiceManager interface {
	EnsureRunning() error
	// RestartIfConfigChanged registers when missing and restarts when the
	// service command or the engine config file contents changed; otherwise
	// ensures it is running. It owns registration.
	RestartIfConfigChanged(binPath, cfgPath string) error
	Stop() error
	Status() (ServiceStatus, error)
}

// OSManager implements ServiceManager by delegating to platform-specific
// logic, ensuring operations are idempotent.
type OSManager struct{}

func (m *OSManager) EnsureRunning() error {
	status, err := serviceStatusCheck()
	if err != nil {
		return err
	}
	if status == ServiceRunning {
		return nil
	}
	return startService()
}

func (m *OSManager) RestartIfConfigChanged(binPath, cfgPath string) error {
	cfg := ServiceConfig{
		BinaryPath: binPath,
		ConfigPath: cfgPath,
		LogDir:     LogDir(),
	}

	desired, err := renderServiceUnit(cfg)
	if err != nil {
		return err
	}

	// Fingerprint = service command + engine config contents. Comparing
	// contents (not just the command) is what makes a reconfigure that only
	// rewrites config.yaml trigger a restart.
	want, err := configFingerprint(desired, cfgPath)
	if err != nil {
		return err
	}
	fpPath := serviceStatePath()
	got, _ := os.ReadFile(fpPath)

	if !bytes.Equal(got, want) {
		slog.Info("service config changed, (re)registering and restarting")
		// Stop before (re)registering: on macOS `launchctl bootstrap` fails
		// ("Bootstrap failed: 5") if the agent is still loaded, so unload it
		// first. Best-effort — a not-running service is not an error here.
		_ = stopService()
		if err := registerService(cfg); err != nil {
			return err
		}
		if err := startService(); err != nil {
			return err
		}
		// Persist the fingerprint only after a successful (re)start, so a
		// failed start is retried on the next apply instead of being masked.
		if err := os.MkdirAll(filepath.Dir(fpPath), 0o755); err == nil {
			if werr := os.WriteFile(fpPath, want, 0o600); werr != nil {
				slog.Warn("could not persist service fingerprint", "error", werr)
			}
		}
		return nil
	}

	return m.EnsureRunning()
}

// configFingerprint hashes the rendered service command together with the
// engine config file contents, so a change to either is detected. A missing
// config file hashes as empty (deterministic).
func configFingerprint(unit []byte, cfgPath string) ([]byte, error) {
	h := sha256.New()
	h.Write(unit)
	if cfgPath != "" {
		content, err := os.ReadFile(cfgPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reading config for fingerprint: %w", err)
		}
		h.Write(content)
	}
	return h.Sum(nil), nil
}

// serviceStatePath is where the last-applied fingerprint is stored.
func serviceStatePath() string {
	return filepath.Join(ConfigDir(), "service-state")
}

func (m *OSManager) Stop() error {
	return stopService()
}

func (m *OSManager) Status() (ServiceStatus, error) {
	return serviceStatusCheck()
}

// renderServiceUnit renders the platform-specific service unit (defined in
// service_<os>.go).
func renderServiceUnit(cfg ServiceConfig) ([]byte, error) {
	return renderUnit(cfg)
}
