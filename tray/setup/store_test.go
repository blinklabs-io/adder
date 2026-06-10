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
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalStoreTrayRoundTrip(t *testing.T) {
	store := &LocalStore{
		TrayConfigPath: filepath.Join(t.TempDir(), "tray", "config.yaml"),
	}
	want := TrayConfig{
		APIAddress:  "127.0.0.1",
		APIPort:     9090,
		AdderConfig: "/tmp/adder/config.yaml",
		AutoStart:   true,
		NotifyPrefs: map[string]bool{
			NotifyPrefIncomingTx: true,
			NotifyPrefVotesCast:  false,
		},
	}

	require.NoError(t, store.SaveTrayAtomic(want))

	got, err := store.LoadTray()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLocalStoreLoadTrayMissingUsesDefaults(t *testing.T) {
	store := &LocalStore{
		TrayConfigPath: filepath.Join(t.TempDir(), "missing.yaml"),
	}

	got, err := store.LoadTray()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", got.APIAddress)
	assert.Equal(t, uint(8080), got.APIPort)
	assert.Empty(t, got.AdderConfig)
}

func TestLocalStoreLoadTrayParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.yaml")
	require.NoError(t, os.WriteFile(path, []byte("api_address: ["), 0o600))
	store := &LocalStore{TrayConfigPath: path}

	_, err := store.LoadTray()
	require.Error(t, err)
	assert.ErrorContains(t, err, "parsing tray config")
}

func TestLocalStoreEngineRoundTrip(t *testing.T) {
	store := &LocalStore{}
	enginePath := filepath.Join(t.TempDir(), "config.yaml")
	plan := SetupPlan{
		Network: NetworkConfig{Name: "preview"},
		Filter: FilterConfig{
			Wallets: []string{
				"addr1qxy648m6k96350t4tql82q0e8sqpks54uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g58xvsf2y066",
			},
		},
		Output: OutputConfig{
			Type: "webhook",
			Config: map[string]string{
				"url":    "https://example.com/webhook",
				"format": "adder",
			},
		},
		API: APIConfig{Address: "127.0.0.1", Port: 9091},
	}
	want := plan.ToEngineConfig(config.Config{})

	require.NoError(t, store.SaveEngineAtomic(enginePath, want))

	got, err := store.LoadEngine(enginePath)
	require.NoError(t, err)
	assert.Equal(t, want.Api.ListenAddress, got.Api.ListenAddress)
	assert.Equal(t, want.Api.ListenPort, got.Api.ListenPort)
	assert.Equal(t, "preview", got.Plugin["input"]["chainsync"]["network"])
	assert.Equal(t, plan.Filter.Wallets[0],
		got.Plugin["filter"]["cardano"]["address"])
	assert.Equal(t, "webhook", got.Output)
	assert.Equal(t, "https://example.com/webhook", got.Plugin["output"]["webhook"]["url"])
}

func TestLocalStoreLoadEngineMissingUsesDefaults(t *testing.T) {
	store := &LocalStore{}

	got, err := store.LoadEngine(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	assert.NotZero(t, got.Api.ListenPort)
}

func TestLocalStoreLoadEngineParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("plugin: ["), 0o600))
	store := &LocalStore{}

	_, err := store.LoadEngine(path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "loading engine config")
}

func TestLocalStoreSaveTrayCreateDirError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o600))
	store := &LocalStore{
		TrayConfigPath: filepath.Join(parent, "tray.yaml"),
	}

	err := store.SaveTrayAtomic(TrayConfig{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "creating config directory")
}
