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
)

// ConfigDir returns the platform-specific directory for storing
// configuration files. It can be overridden by the ADDER_TRAY_CONFIG_DIR
// environment variable.
func ConfigDir() string {
	if val := os.Getenv("ADDER_TRAY_CONFIG_DIR"); val != "" {
		return val
	}
	return configDir()
}

// LogDir returns the platform-specific directory for storing log files.
// It can be overridden by the ADDER_TRAY_LOG_DIR environment variable.
func LogDir() string {
	if val := os.Getenv("ADDER_TRAY_LOG_DIR"); val != "" {
		return val
	}
	return logDir()
}

// ExpandTildePath expands the '~' prefix in a file path to the user's home
// directory. Returns the original path if no expansion is needed.
func ExpandTildePath(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path, fmt.Errorf("failed to get user home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	if len(path) > 1 && (path[1] == '/' || path[1] == '\\') {
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
