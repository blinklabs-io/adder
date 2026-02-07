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

package tray

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDir(t *testing.T) {
	dir := ConfigDir()
	require.NotEmpty(t, dir, "ConfigDir must not be empty")
	assert.True(
		t,
		filepath.IsAbs(dir),
		"ConfigDir must return an absolute path",
	)
	assert.True(
		t,
		strings.Contains(strings.ToLower(dir), "adder"),
		"ConfigDir should contain 'adder' in the path",
	)
}

func TestLogDir(t *testing.T) {
	dir := LogDir()
	require.NotEmpty(t, dir, "LogDir must not be empty")
	assert.True(
		t,
		filepath.IsAbs(dir),
		"LogDir must return an absolute path",
	)
	assert.True(
		t,
		strings.Contains(strings.ToLower(dir), "adder"),
		"LogDir should contain 'adder' in the path",
	)
}

func TestPathsArePlatformAppropriate(t *testing.T) {
	configDir := ConfigDir()
	logDir := LogDir()

	switch runtime.GOOS {
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			assert.True(t, strings.HasPrefix(configDir, xdg),
				"ConfigDir should start with XDG_CONFIG_HOME when set")
		} else {
			assert.Contains(t, configDir, ".config")
		}
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			assert.True(t, strings.HasPrefix(logDir, xdg),
				"LogDir should start with XDG_STATE_HOME when set")
		} else {
			assert.Contains(t, logDir, ".local")
		}
	case "darwin":
		assert.Contains(t, configDir, "Library")
		assert.Contains(t, logDir, "Library")
	case "windows":
		assert.Contains(t, configDir, "Adder")
		assert.Contains(t, logDir, "Adder")
	}
}
