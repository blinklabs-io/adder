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
	"time"

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
	assert.Equal(t,
		"preview", got.Plugin["input"]["chainsync"]["network"])
	// The engine config no longer carries per-target filter knobs —
	// they live on TrayConfig.Filter instead so a multi-target plan
	// cannot be ANDed together by the sidecar's cardano filter.
	if cardano, ok := got.Plugin["filter"]["cardano"]; ok {
		assert.NotContains(t, cardano, "address")
		assert.NotContains(t, cardano, "drep")
		assert.NotContains(t, cardano, "pool")
	}
	assert.Equal(t, "webhook", got.Output)
	assert.Equal(t, "https://example.com/webhook",
		got.Plugin["output"]["webhook"]["url"])
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

// TestResolvedNotifyRate locks in the zero-value-resolves-to-default
// behaviour plus the negative-disables and explicit-override paths so
// a user's YAML knob actually takes effect without surprising
// override-by-zero edge cases.
func TestResolvedNotifyRate(t *testing.T) {
	cases := []struct {
		name       string
		cfg        TrayConfig
		wantLimit  int
		wantWindow time.Duration
	}{
		{
			name:       "zero values use defaults",
			cfg:        TrayConfig{},
			wantLimit:  DefaultNotifyRateLimit,
			wantWindow: DefaultNotifyRateWindow,
		},
		{
			name: "explicit values pass through",
			cfg: TrayConfig{
				NotifyRateLimit:  10,
				NotifyRateWindow: 30 * time.Second,
			},
			wantLimit:  10,
			wantWindow: 30 * time.Second,
		},
		{
			name: "negative limit disables coalescing",
			cfg: TrayConfig{
				NotifyRateLimit:  -1,
				NotifyRateWindow: 2 * time.Second,
			},
			wantLimit:  -1,
			wantWindow: 2 * time.Second,
		},
		{
			name: "zero window uses default with custom limit",
			cfg: TrayConfig{
				NotifyRateLimit: 5,
			},
			wantLimit:  5,
			wantWindow: DefaultNotifyRateWindow,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			limit, window := c.cfg.ResolvedNotifyRate()
			assert.Equal(t, c.wantLimit, limit)
			assert.Equal(t, c.wantWindow, window)
		})
	}
}
