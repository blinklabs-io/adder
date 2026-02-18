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
	"gopkg.in/yaml.v3"
)

func TestMonitorTemplateString(t *testing.T) {
	tests := []struct {
		tmpl MonitorTemplate
		want string
	}{
		{WatchWallet, "Watch Wallet"},
		{TrackDRep, "Track DRep"},
		{MonitorPool, "Monitor Pool"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.tmpl.String())
	}
}

func TestGenerateAdderConfig_WatchWallet(t *testing.T) {
	params := AdderConfigParams{
		Network:  "mainnet",
		Template: WatchWallet,
		Param:    "addr1qtest123",
	}
	data, err := GenerateAdderConfig(params)
	require.NoError(t, err)

	// Unmarshal back and verify structure
	var cfg map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &cfg))

	assert.Equal(t, "chainsync", cfg["input"])
	assert.Equal(t, "log", cfg["output"])

	// Verify API config
	api, ok := cfg["api"].(map[string]interface{})
	require.True(t, ok, "api should be a map")
	assert.Equal(t, "127.0.0.1", api["address"])
	assert.Equal(t, 8080, api["port"])

	// Verify plugins structure
	plugins, ok := cfg["plugins"].(map[string]interface{})
	require.True(t, ok, "plugins should be a map")
	input, ok := plugins["input"].(map[string]interface{})
	require.True(t, ok, "plugins.input should be a map")
	chainsync, ok := input["chainsync"].(map[string]interface{})
	require.True(t, ok, "plugins.input.chainsync should be a map")
	assert.Equal(t, "mainnet", chainsync["network"])

	// Verify filter
	filter, ok := plugins["filter"].(map[string]interface{})
	require.True(t, ok, "plugins.filter should be a map")
	filterChainsync, ok := filter["chainsync"].(map[string]interface{})
	require.True(t, ok, "plugins.filter.chainsync should be a map")
	assert.Equal(t, "addr1qtest123", filterChainsync["address"])
}

func TestGenerateAdderConfig_TrackDRep(t *testing.T) {
	params := AdderConfigParams{
		Network:  "preview",
		Template: TrackDRep,
		Param:    "drep1test456",
	}
	data, err := GenerateAdderConfig(params)
	require.NoError(t, err)

	var cfg map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &cfg))

	plugins, ok := cfg["plugins"].(map[string]interface{})
	require.True(t, ok, "plugins should be a map")
	filter, ok := plugins["filter"].(map[string]interface{})
	require.True(t, ok, "plugins.filter should be a map")
	filterChainsync, ok := filter["chainsync"].(map[string]interface{})
	require.True(t, ok, "plugins.filter.chainsync should be a map")
	assert.Equal(t, "drep1test456", filterChainsync["drep"])
}

func TestGenerateAdderConfig_MonitorPool(t *testing.T) {
	params := AdderConfigParams{
		Network:  "mainnet",
		Template: MonitorPool,
		Param:    "pool1test789",
	}
	data, err := GenerateAdderConfig(params)
	require.NoError(t, err)

	var cfg map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &cfg))

	plugins, ok := cfg["plugins"].(map[string]interface{})
	require.True(t, ok, "plugins should be a map")
	filter, ok := plugins["filter"].(map[string]interface{})
	require.True(t, ok, "plugins.filter should be a map")
	filterChainsync, ok := filter["chainsync"].(map[string]interface{})
	require.True(t, ok, "plugins.filter.chainsync should be a map")
	assert.Equal(t, "pool1test789", filterChainsync["pool"])
}

func TestGenerateAdderConfig_CustomOutput(t *testing.T) {
	params := AdderConfigParams{
		Network:  "mainnet",
		Template: WatchWallet,
		Param:    "addr1qtest",
		Output:   "webhook",
		Format:   "jsonl",
	}
	data, err := GenerateAdderConfig(params)
	require.NoError(t, err)

	var cfg map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &cfg))

	assert.Equal(t, "webhook", cfg["output"])
	plugins, ok := cfg["plugins"].(map[string]interface{})
	require.True(t, ok, "plugins should be a map")
	output, ok := plugins["output"].(map[string]interface{})
	require.True(t, ok, "plugins.output should be a map")
	webhook, ok := output["webhook"].(map[string]interface{})
	require.True(t, ok, "plugins.output.webhook should be a map")
	assert.Equal(t, "jsonl", webhook["format"])
}

func TestGenerateAdderConfig_NetworkRequired(t *testing.T) {
	params := AdderConfigParams{
		Template: WatchWallet,
		Param:    "addr1qtest",
	}
	_, err := GenerateAdderConfig(params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network")
}

func TestGenerateAdderConfig_ParamRequired(t *testing.T) {
	params := AdderConfigParams{
		Network:  "mainnet",
		Template: WatchWallet,
	}
	_, err := GenerateAdderConfig(params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parameter")
}

func TestGenerateAdderConfig_RoundTrip(t *testing.T) {
	params := AdderConfigParams{
		Network:  "mainnet",
		Template: WatchWallet,
		Param:    "addr1qtest",
	}
	data, err := GenerateAdderConfig(params)
	require.NoError(t, err)

	// Should be valid YAML that can be unmarshaled and re-marshaled
	var intermediate interface{}
	require.NoError(t, yaml.Unmarshal(data, &intermediate))
	data2, err := yaml.Marshal(intermediate)
	require.NoError(t, err)
	assert.NotEmpty(t, data2)
}

func TestAdderConfigPath(t *testing.T) {
	path := AdderConfigPath()
	assert.True(t, filepath.IsAbs(path))
	assert.Equal(t, "config.yaml", filepath.Base(path))
}

func TestWriteAdderConfig(t *testing.T) {
	tmpDir := t.TempDir()
	switch runtime.GOOS {
	case "linux":
		t.Setenv("XDG_CONFIG_HOME", tmpDir)
	case "darwin":
		t.Setenv("HOME", tmpDir)
	case "windows":
		t.Setenv("APPDATA", tmpDir)
	default:
		t.Skipf("unsupported platform: %s", runtime.GOOS)
	}

	params := AdderConfigParams{
		Network:  "mainnet",
		Template: WatchWallet,
		Param:    "addr1qtest",
	}

	err := WriteAdderConfig(params)
	require.NoError(t, err)

	// Verify file exists
	path := AdderConfigPath()
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	// Verify content is valid YAML
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	assert.Equal(t, "chainsync", cfg["input"])
}

func TestTemplateFilterKey(t *testing.T) {
	assert.Equal(t, "address", templateFilterKey(WatchWallet))
	assert.Equal(t, "drep", templateFilterKey(TrackDRep))
	assert.Equal(t, "pool", templateFilterKey(MonitorPool))
	assert.Equal(t, "", templateFilterKey(MonitorTemplate(99)))
}

func TestGenerateAdderConfig_UnsupportedTemplate(t *testing.T) {
	params := AdderConfigParams{
		Network:  "mainnet",
		Template: MonitorTemplate(99),
		Param:    "test",
	}
	_, err := GenerateAdderConfig(params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported monitor template")
}
