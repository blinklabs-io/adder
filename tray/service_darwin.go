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

const launchAgentLabel = "io.blinklabs.adder"
const launchAgentFile = "io.blinklabs.adder.plist"

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

	if err := os.WriteFile(servicePlistPath(), data, 0o644); err != nil {
		return fmt.Errorf("writing plist file: %w", err)
	}

	if out, err := exec.Command(
		"launchctl", "load", "-w", servicePlistPath(),
	).CombinedOutput(); err != nil {
		return fmt.Errorf("loading launch agent: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func unregisterService() error {
	if out, err := exec.Command(
		"launchctl", "unload", servicePlistPath(),
	).CombinedOutput(); err != nil {
		return fmt.Errorf("unloading launch agent: %s: %w", strings.TrimSpace(string(out)), err)
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
