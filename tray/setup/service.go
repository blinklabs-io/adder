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
	EnsureRegistered(binPath, cfgPath string) error
	EnsureRunning() error
	RestartIfConfigChanged(binPath, cfgPath string) error
	Stop() error
	Status() (ServiceStatus, error)
}

// registrar seams the platform registration primitives so EnsureRegistered's
// idempotent skip/repair decision is testable on any host OS.
type registrar interface {
	existingUnit() []byte
	registerService(cfg ServiceConfig) error
}

// osRegistrar is the default backend: it delegates to the per-OS free
// functions (existingUnit/registerService in service_<os>.go).
type osRegistrar struct{}

func (osRegistrar) existingUnit() []byte { return existingUnit() }

func (osRegistrar) registerService(c ServiceConfig) error {
	return registerService(c)
}

// OSManager implements ServiceManager by delegating to platform-specific
// logic, ensuring operations are idempotent.
type OSManager struct {
	// reg is the registration backend; nil means the real platform functions
	// (osRegistrar). Tests inject a fake to observe skip vs repair.
	reg registrar
}

// registrar returns the injected backend, or the real OS-backed default so a
// zero-value &OSManager{} keeps working.
func (m *OSManager) registrar() registrar {
	if m.reg != nil {
		return m.reg
	}
	return osRegistrar{}
}

func (m *OSManager) EnsureRegistered(binPath, cfgPath string) error {
	cfg := ServiceConfig{
		BinaryPath: binPath,
		ConfigPath: cfgPath,
		LogDir:     LogDir(),
	}

	desired, err := renderUnit(cfg)
	if err != nil {
		return fmt.Errorf("rendering service unit: %w", err)
	}

	// Re-register unless the platform registration already matches the
	// desired state. Each OS owns how to inspect that registration.
	r := m.registrar()
	if existing := r.existingUnit(); existing != nil &&
		bytes.Equal(existing, desired) {
		slog.Debug("service already registered with identical configuration")
		return nil
	}

	slog.Info("registering/updating system service")
	return r.registerService(cfg)
}

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

	desired, err := renderUnit(cfg)
	if err != nil {
		return err
	}

	// Restart when the rendered unit OR the engine config.yaml contents have
	// changed since the last successful apply. The unit references the config
	// by path, not contents, so hashing the config bytes is what makes a
	// same-path config edit (network, filters, …) take effect. Registration
	// itself is owned by EnsureRegistered, which the caller runs first.
	want, err := configFingerprint(desired, cfgPath)
	if err != nil {
		return err
	}
	got, _ := os.ReadFile(serviceStatePath())

	if !bytes.Equal(got, want) {
		slog.Info("service configuration changed, restarting")
		// Stop before start so the engine actually reloads; service managers
		// can otherwise leave the old instance running. Best-effort: a
		// not-running service is not an error here.
		_ = stopService()
		if err := startService(); err != nil {
			return err
		}
		// Persist only after a successful restart so a failed start is retried
		// on the next apply instead of being masked as already-applied.
		if err := os.MkdirAll(filepath.Dir(serviceStatePath()), 0o755); err == nil {
			if werr := os.WriteFile(serviceStatePath(), want, 0o600); werr != nil {
				slog.Warn(
					"could not persist service fingerprint",
					"error",
					werr,
				)
			}
		}
		return nil
	}

	return m.EnsureRunning()
}

func (m *OSManager) Stop() error {
	return stopService()
}

func (m *OSManager) Status() (ServiceStatus, error) {
	return serviceStatusCheck()
}

// configFingerprint hashes the rendered service unit together with the engine
// config.yaml contents, so a change to either is detected. The unit only
// references the config by path, so hashing the contents is what catches a
// same-path config edit. A missing config file hashes as empty (deterministic).
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

// serviceStatePath is where the last-applied restart fingerprint is stored.
func serviceStatePath() string {
	return filepath.Join(ConfigDir(), "service-state")
}
