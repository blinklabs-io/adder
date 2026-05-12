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

//go:build darwin

package tray

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	launchAgentLabel = "io.blinklabs.adder"
	launchAgentFile  = "io.blinklabs.adder.plist"
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.blinklabs.adder</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath | xmlEscape}}</string>
        {{- if .ConfigPath}}
        <string>--config</string>
        <string>{{.ConfigPath | xmlEscape}}</string>
        {{- end}}
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogDir | xmlEscape}}/adder.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir | xmlEscape}}/adder.stderr.log</string>
</dict>
</plist>
`

// servicePlistDir returns the LaunchAgents directory.
func servicePlistDir() string {
	return filepath.Join(homeOrTmp(), "Library", "LaunchAgents")
}

// servicePlistPath returns the full path to the adder LaunchAgent plist.
func servicePlistPath() string {
	return filepath.Join(servicePlistDir(), launchAgentFile)
}

// xmlEscapeString returns s with XML-special characters escaped.
func xmlEscapeString(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

// renderPlist renders the LaunchAgent plist template with the given config.
func renderPlist(cfg ServiceConfig) ([]byte, error) {
	funcMap := template.FuncMap{
		"xmlEscape": xmlEscapeString,
	}
	tmpl, err := template.New("plist").Funcs(funcMap).Parse(plistTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing plist template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return nil, fmt.Errorf("rendering plist template: %w", err)
	}
	return buf.Bytes(), nil
}

func registerService(cfg ServiceConfig) error {
	if cfg.LogDir == "" {
		cfg.LogDir = LogDir()
	}

	plistDir := servicePlistDir()
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents dir: %w", err)
	}

	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	data, err := renderPlist(cfg)
	if err != nil {
		return err
	}

	if err := os.WriteFile(servicePlistPath(), data, 0o644); err != nil { //nolint:gosec // plist files need 0644 permissions
		return fmt.Errorf("writing plist file: %w", err)
	}

	target := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := exec.Command( //nolint:gosec // paths are generated internally
		"launchctl", "bootstrap", target, servicePlistPath(),
	).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "service already bootstrapped") {
			return fmt.Errorf("loading launch agent: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	return nil
}

func unregisterService() error {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)
	if out, err := exec.Command( //nolint:gosec // paths are generated internally
		"launchctl", "bootout", target,
	).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "Could not find service") {
			return fmt.Errorf("unloading launch agent: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	if err := os.Remove(servicePlistPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist file: %w", err)
	}

	return nil
}

func serviceStatusCheck() (ServiceStatus, error) {
	if _, err := os.Stat(servicePlistPath()); os.IsNotExist(err) {
		return ServiceNotRegistered, nil
	}

	if err := exec.Command("launchctl", "list", launchAgentLabel).Run(); err == nil {
		return ServiceRunning, nil
	}

	return ServiceRegistered, nil
}

func startService() error {
	target := fmt.Sprintf("gui/%d", os.Getuid())
	if out, err := exec.Command( //nolint:gosec // paths are generated internally
		"launchctl", "bootstrap", target, servicePlistPath(),
	).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "service already bootstrapped") {
			return fmt.Errorf("starting launch agent: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

func stopService() error {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)
	if out, err := exec.Command( //nolint:gosec // paths are generated internally
		"launchctl", "bootout", target,
	).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "Could not find service") {
			return fmt.Errorf("stopping launch agent: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}
