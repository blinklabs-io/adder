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

//go:build windows

package tray

import (
	"fmt"
	"os/exec"
	"strings"
)

const taskName = "Adder"

func registerService(cfg ServiceConfig) error {
	command := fmt.Sprintf(`"%s"`, cfg.BinaryPath)
	if cfg.ConfigPath != "" {
		command = fmt.Sprintf(`"%s" --config "%s"`, cfg.BinaryPath, cfg.ConfigPath)
	}

	out, err := exec.Command( //nolint:gosec // command args constructed from validated config
		"schtasks.exe",
		"/Create",
		"/TN", taskName,
		"/SC", "ONLOGON",
		"/TR", command,
		"/RL", "LIMITED",
		"/F",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("creating scheduled task: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func unregisterService() error {
	out, err := exec.Command(
		"schtasks.exe",
		"/Delete",
		"/TN", taskName,
		"/F",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("deleting scheduled task: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func serviceStatusCheck() (ServiceStatus, error) {
	out, err := exec.Command(
		"schtasks.exe",
		"/Query",
		"/TN", taskName,
		"/FO", "CSV",
		"/NH",
	).CombinedOutput()
	if err != nil {
		outStr := strings.ToLower(strings.TrimSpace(string(out)))
		// schtasks returns exit code 1 when the task does not exist;
		// treat only that case as "not registered" and propagate all
		// other errors (access denied, I/O failures, etc.).
		if strings.Contains(outStr, "does not exist") ||
			strings.Contains(outStr, "cannot find") {
			return ServiceNotRegistered, nil
		}
		return ServiceNotRegistered, fmt.Errorf(
			"querying scheduled task: %s: %w",
			strings.TrimSpace(string(out)),
			err,
		)
	}

	if strings.Contains(string(out), "Running") {
		return ServiceRunning, nil
	}

	return ServiceRegistered, nil
}
