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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigPath(t *testing.T) {
	path := ConfigPath()
	require.NotEmpty(t, path, "ConfigPath must not be empty")
	assert.True(
		t,
		filepath.IsAbs(path),
		"ConfigPath must return an absolute path",
	)
	assert.Equal(
		t,
		configFileName,
		filepath.Base(path),
		"ConfigPath must end with the config file name",
	)
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, "127.0.0.1", cfg.APIAddress)
	assert.Equal(t, uint(8080), cfg.APIPort)
	assert.Equal(t, "", cfg.AdderConfig)
	assert.False(t, cfg.AutoStart)
}

func TestLoadConfigMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CONFIG_HOME only applies on Linux")
	}
	// When no config file exists, LoadConfig should return defaults
	// without error. We set XDG_CONFIG_HOME to a temp dir so we
	// don't accidentally read a real config.
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig(), cfg)
}

func TestSaveAndLoadConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CONFIG_HOME only applies on Linux")
	}
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	original := TrayConfig{
		APIAddress:  "192.168.1.100",
		APIPort:     9090,
		AdderConfig: "/etc/adder/config.yaml",
		AutoStart:   true,
	}

	err := SaveConfig(original)
	require.NoError(t, err)

	// Verify the file was created
	_, err = os.Stat(ConfigPath())
	require.NoError(t, err, "config file should exist after save")

	loaded, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, original, loaded)
}

func TestLoadConfigInvalidAddress(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CONFIG_HOME only applies on Linux")
	}
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Write a config with empty api_address
	err := SaveConfig(TrayConfig{
		APIAddress: "placeholder",
		APIPort:    8080,
	})
	require.NoError(t, err)

	// Overwrite with invalid content
	err = os.WriteFile(
		ConfigPath(),
		[]byte("api_address: \"\"\napi_port: 8080\n"),
		0o600,
	)
	require.NoError(t, err)

	_, err = LoadConfig()
	assert.ErrorContains(t, err, "api_address")
}

func TestLoadConfigInvalidPort(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CONFIG_HOME only applies on Linux")
	}
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700)
	require.NoError(t, err)

	err = os.WriteFile(
		ConfigPath(),
		[]byte("api_address: \"127.0.0.1\"\napi_port: 0\n"),
		0o600,
	)
	require.NoError(t, err)

	_, err = LoadConfig()
	assert.ErrorContains(t, err, "api_port")
}

func TestConfigExists(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CONFIG_HOME only applies on Linux")
	}
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	assert.False(
		t,
		ConfigExists(),
		"ConfigExists should be false before save",
	)

	err := SaveConfig(DefaultConfig())
	require.NoError(t, err)

	assert.True(
		t,
		ConfigExists(),
		"ConfigExists should be true after save",
	)
}
