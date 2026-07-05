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
	"fmt"
	"log/slog"
	"os"
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

// OSManager implements ServiceManager by delegating to platform-specific
// logic, ensuring operations are idempotent.
type OSManager struct{}

func (m *OSManager) EnsureRegistered(binPath, cfgPath string) error {
	cfg := ServiceConfig{
		BinaryPath: binPath,
		ConfigPath: cfgPath,
		LogDir:     LogDir(),
	}

	// 1. Render desired state
	desired, err := renderServiceUnit(cfg)
	if err != nil {
		return fmt.Errorf("rendering service unit: %w", err)
	}

	// 2. Check existing state
	path := serviceUnitFilePath()
	existing, _ := os.ReadFile(path)

	if existing != nil && bytes.Equal(existing, desired) {
		slog.Debug("service already registered with identical configuration",
			"path", path)
		return nil
	}

	// 3. Update only if different
	slog.Info("registering/updating system service", "path", path)
	return registerService(cfg)
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

	desired, err := renderServiceUnit(cfg)
	if err != nil {
		return err
	}

	path := serviceUnitFilePath()
	existing, _ := os.ReadFile(path)

	if len(existing) > 0 && !bytes.Equal(existing, desired) {
		slog.Info("service configuration changed, restarting", "path", path)
		if err := registerService(cfg); err != nil {
			return err
		}
		return startService()
	}

	return m.EnsureRunning()
}

func (m *OSManager) Stop() error {
	return stopService()
}

func (m *OSManager) Status() (ServiceStatus, error) {
	return serviceStatusCheck()
}

// Helper to provide uniform access to platform-specific unit rendering
func renderServiceUnit(cfg ServiceConfig) ([]byte, error) {
	// These are defined in service_<os>.go
	return renderUnit(cfg)
}

func serviceUnitFilePath() string {
	// This is defined in service_<os>.go
	return serviceUnitPath()
}
