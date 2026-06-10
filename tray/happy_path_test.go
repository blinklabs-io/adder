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
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestHappyPath_WizardFinish(t *testing.T) {
	// 1. Setup isolated environment
	tmpDir := t.TempDir()
	t.Setenv("ADDER_TRAY_CONFIG_DIR", tmpDir)
	t.Setenv("ADDER_TRAY_LOG_DIR", tmpDir)

	// 2. Initialize App
	fyneApp := test.NewApp()
	a, err := NewApp(fyneApp)
	require.NoError(t, err)

	// 3. Define a "Happy Path" Setup Plan
	plan := setup.SetupPlan{
		Network: setup.NetworkConfig{Name: "preprod"},
		Filter: setup.FilterConfig{
			Wallets: []string{
				"addr1qxy648m6k96350t4tql82q0e8sqpks54uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g58xvsf2y066",
			},
		},
		API: setup.APIConfig{
			Address: "127.0.0.1",
			Port:    9090, // Use different port to avoid conflict
		},
		Output: setup.OutputConfig{
			Type:   "none",
			Config: make(map[string]string),
		},
		Notify: setup.NotificationPrefs{
			"Incoming transactions": true,
		},
		App: setup.AppConfig{
			AutoStart: true,
		},
	}

	// 4. Simulate Wizard Finish
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	// Note: onWizardFinish runs tasks in a goroutine.
	a.onWizardFinish(ctx, plan, nil)

	// Wait for background tasks to complete by polling for the expected files
	engineCfgPath := filepath.Join(tmpDir, "config.yaml")
	trayCfgPath := ConfigPath()
	success := false
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(engineCfgPath); err == nil {
			if _, err := os.Stat(trayCfgPath); err == nil {
				success = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel() // Done with context
	require.True(t, success, "configuration files were not created in time")

	// 5. Verify Engine Config
	assert.FileExists(t, engineCfgPath)
	assert.FileExists(t, trayCfgPath)

	engineData, err := os.ReadFile(engineCfgPath)
	require.NoError(t, err)

	var engineCfg config.Config
	err = yaml.Unmarshal(engineData, &engineCfg)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", engineCfg.Api.ListenAddress)
	assert.Equal(t, uint(9090), engineCfg.Api.ListenPort)
	assert.Equal(t, "preprod", engineCfg.Plugin["input"]["chainsync"]["network"])
	assert.Equal(t, plan.Filter.Wallets[0],
		engineCfg.Plugin["filter"]["cardano"]["address"])

	// 6. Verify Tray Config
	trayData, err := os.ReadFile(trayCfgPath)
	require.NoError(t, err)

	var trayCfg TrayConfig
	err = yaml.Unmarshal(trayData, &trayCfg)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", trayCfg.APIAddress)
	assert.Equal(t, uint(9090), trayCfg.APIPort)
	assert.Equal(t, engineCfgPath, trayCfg.AdderConfig)
	assert.True(t, trayCfg.NotifyPrefs["Incoming transactions"])
}
