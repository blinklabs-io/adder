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
	"log/slog"
	"os"
	"path/filepath"
)

func homeOrTmp() string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn(
			"unable to determine home directory, falling back to os.TempDir()",
			"error", err,
		)
		return os.TempDir()
	}
	return home
}

func configDir() string {
	return filepath.Join(
		homeOrTmp(), "Library", "Application Support", "Adder",
	)
}

func logDir() string {
	return filepath.Join(homeOrTmp(), "Library", "Logs", "Adder")
}
