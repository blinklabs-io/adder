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
	"errors"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reconfigureStore struct {
	engine     config.Config
	loadErr    error
	loadedPath string
}

func (s *reconfigureStore) LoadEngine(path string) (config.Config, error) {
	s.loadedPath = path
	if s.loadErr != nil {
		return config.Config{}, s.loadErr
	}
	return s.engine, nil
}

func (s *reconfigureStore) SaveEngineAtomic(string, config.Config) error {
	return nil
}

func (s *reconfigureStore) LoadTray() (setup.TrayConfig, error) {
	return setup.TrayConfig{}, nil
}

func (s *reconfigureStore) SaveTrayAtomic(setup.TrayConfig) error {
	return nil
}

func TestReconfigurePlanLoadsSavedEngineConfig(t *testing.T) {
	savedPath := filepath.Join(t.TempDir(), "config.yaml")
	sourcePlan := setup.SetupPlan{
		Network: setup.NetworkConfig{Name: "preprod"},
		Filter: setup.FilterConfig{
			DReps: []string{
				"drep1qxy648m6k96350t4tql82q0e8sqpks54uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g58xvsf2y066",
			},
		},
		Output: setup.OutputConfig{
			Type: "webhook",
			Config: map[string]string{
				"url":    "https://example.com/webhook",
				"format": "discord",
			},
		},
		API: setup.APIConfig{Address: "127.0.0.1", Port: 9090},
	}
	store := &reconfigureStore{
		engine: sourcePlan.ToEngineConfig(*config.GetConfig()),
	}
	app := &App{
		config: TrayConfig{
			AdderConfig: savedPath,
			AutoStart:   true,
			NotifyPrefs: map[string]bool{
				setup.NotifyPrefVotesCast: true,
			},
		},
		runner: &setup.SetupRunner{Store: store},
	}

	got, err := app.reconfigurePlan()
	require.NoError(t, err)

	assert.Equal(t, savedPath, store.loadedPath)
	assert.Equal(t, sourcePlan.Network.Name, got.Network.Name)
	assert.Equal(t, sourcePlan.Filter.MonitorEverything,
		got.Filter.MonitorEverything)
	assert.Equal(t, sourcePlan.Filter.Wallets, got.Filter.Wallets)
	assert.Equal(t, sourcePlan.Filter.DReps, got.Filter.DReps)
	assert.Equal(t, sourcePlan.Filter.Pools, got.Filter.Pools)
	assert.Equal(t, sourcePlan.Output.Type, got.Output.Type)
	assert.Equal(t, sourcePlan.Output.Config, got.Output.Config)
	assert.Equal(t, sourcePlan.API.Address, got.API.Address)
	assert.Equal(t, sourcePlan.API.Port, got.API.Port)
	assert.True(t, got.App.AutoStart)
	assert.True(t, got.Notify[setup.NotifyPrefVotesCast])
}

func TestReconfigurePlanReturnsLoadEngineError(t *testing.T) {
	wantErr := errors.New("boom")
	store := &reconfigureStore{loadErr: wantErr}
	app := &App{
		config: TrayConfig{AdderConfig: "/tmp/adder/config.yaml"},
		runner: &setup.SetupRunner{Store: store},
	}

	_, err := app.reconfigurePlan()
	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, app.config.AdderConfig, store.loadedPath)
}
