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

func TestServiceStatusString(t *testing.T) {
	tests := []struct {
		status ServiceStatus
		want   string
	}{
		{ServiceNotRegistered, "not registered"},
		{ServiceRegistered, "registered"},
		{ServiceRunning, "running"},
		{ServiceStatus(99), "unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.status.String())
	}
}

func TestServiceConfigValidate(t *testing.T) {
	t.Run("empty binary path", func(t *testing.T) {
		cfg := ServiceConfig{}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "binary path")
	})

	t.Run("valid config", func(t *testing.T) {
		cfg := ServiceConfig{BinaryPath: "/usr/bin/adder"}
		err := cfg.Validate()
		require.NoError(t, err)
	})
}

func TestRegisterServiceValidation(t *testing.T) {
	err := RegisterService(ServiceConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary path")
}

func TestServiceUnitDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd unit dir only applies on Linux")
	}

	t.Run("uses XDG_CONFIG_HOME", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		dir := serviceUnitDir()
		assert.True(
			t,
			strings.HasPrefix(dir, tmpDir),
			"serviceUnitDir should start with XDG_CONFIG_HOME",
		)
		assert.True(
			t,
			strings.HasSuffix(dir, filepath.Join("systemd", "user")),
			"serviceUnitDir should end with systemd/user",
		)
	})

	t.Run("fallback without XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")

		dir := serviceUnitDir()
		assert.Contains(t, dir, ".config")
		assert.Contains(t, dir, filepath.Join("systemd", "user"))
	})
}

func TestServiceUnitPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd unit path only applies on Linux")
	}

	path := serviceUnitPath()
	assert.True(
		t,
		strings.HasSuffix(path, "adder.service"),
		"serviceUnitPath should end with adder.service",
	)
}

func TestServiceUnitTemplate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd unit template only applies on Linux")
	}

	cfg := ServiceConfig{
		BinaryPath: "/usr/bin/adder",
		ConfigPath: "/etc/adder.yaml",
	}

	data, err := renderUnit(cfg)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[Unit]")
	assert.Contains(t, content, "[Service]")
	assert.Contains(t, content, "[Install]")
	assert.Contains(
		t, content,
		"ExecStart=/usr/bin/adder --config /etc/adder.yaml",
	)
	assert.Contains(t, content, "Restart=on-failure")
	assert.Contains(t, content, "WantedBy=default.target")

	// Verify it can be written and read back
	tmpDir := t.TempDir()
	unitPath := filepath.Join(tmpDir, "adder.service")
	err = os.WriteFile(unitPath, data, 0o644)
	require.NoError(t, err)

	readBack, err := os.ReadFile(unitPath)
	require.NoError(t, err)
	assert.Equal(t, content, string(readBack))
}

func TestServiceUnitTemplateNoConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd unit template only applies on Linux")
	}

	cfg := ServiceConfig{
		BinaryPath: "/usr/bin/adder",
	}

	data, err := renderUnit(cfg)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "ExecStart=/usr/bin/adder")
	assert.NotContains(t, content, "--config")
}
