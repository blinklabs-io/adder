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

//go:build linux

package tray

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const serviceName = "adder.service"

const serviceUnitTemplate = `[Unit]
Description=Adder - Cardano Event Streamer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart="{{.BinaryPath}}"{{if .ConfigPath}} --config "{{.ConfigPath}}"{{end}}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
Environment=HOME=%h

[Install]
WantedBy=default.target
`

// serviceUnitDir returns the systemd user unit directory.
func serviceUnitDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "systemd", "user")
	}
	return filepath.Join(homeOrTmp(), ".config", "systemd", "user")
}

// serviceUnitPath returns the full path to the adder systemd unit file.
func serviceUnitPath() string {
	return filepath.Join(serviceUnitDir(), serviceName)
}

// renderUnit renders the systemd unit template with the given config.
func renderUnit(cfg ServiceConfig) ([]byte, error) {
	tmpl, err := template.New("unit").Parse(serviceUnitTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing unit template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return nil, fmt.Errorf("rendering unit template: %w", err)
	}
	return buf.Bytes(), nil
}

func registerService(cfg ServiceConfig) error {
	unitDir := serviceUnitDir()
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("creating systemd user dir: %w", err)
	}

	data, err := renderUnit(cfg)
	if err != nil {
		return err
	}

	if err := os.WriteFile(serviceUnitPath(), data, 0o644); err != nil { //nolint:gosec // systemd unit files need 0644 permissions
		return fmt.Errorf("writing unit file: %w", err)
	}

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)
	}

	if out, err := exec.Command("systemctl", "--user", "enable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("enabling service: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func unregisterService() error {
	if out, err := exec.Command("systemctl", "--user", "disable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("disabling service: %s: %w", strings.TrimSpace(string(out)), err)
	}

	if err := os.Remove(serviceUnitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file: %w", err)
	}

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func serviceStatusCheck() (ServiceStatus, error) {
	if _, err := os.Stat(serviceUnitPath()); os.IsNotExist(err) {
		return ServiceNotRegistered, nil
	}

	out, err := exec.Command("systemctl", "--user", "is-active", serviceName).Output()
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		return ServiceRunning, nil
	}

	return ServiceRegistered, nil
}
