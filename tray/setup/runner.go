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
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"
)

// Connector defines the interface for managing the API connection.
type Connector interface {
	Connect() error
	Disconnect()
	Reconnect() error
	SetAddress(addr string)
	SetPort(port uint)
}

// SetupRunner orchestrates the application of a SetupPlan.
type SetupRunner struct {
	Store   ConfigStore
	Service ServiceManager
	Conn    Connector
	Finder  BinaryFinder
}

// BinaryFinder defines the interface for locating the adder binary.
type BinaryFinder interface {
	Find() (string, error)
}

func (r *SetupRunner) Apply(ctx context.Context, plan SetupPlan) error {
	slog.Info("applying setup plan")

	// 1. Prepare engine config
	engineCfg, err := r.Store.LoadEngine(filepath.Join(ConfigDir(), "config.yaml"))
	if err != nil {
		return err
	}
	engineCfg = plan.ToEngineConfig(engineCfg)

	// 2. Save Engine Config
	engineCfgPath := filepath.Join(ConfigDir(), "config.yaml")
	if err := r.Store.SaveEngineAtomic(engineCfgPath, engineCfg); err != nil {
		return fmt.Errorf("saving engine config: %w", err)
	}

	// 3. Save Tray Config
	trayCfg := TrayConfig{
		APIAddress:  engineCfg.Api.ListenAddress,
		APIPort:     engineCfg.Api.ListenPort,
		AdderConfig: engineCfgPath,
		AutoStart:   plan.App.AutoStart,
		NotifyPrefs: map[string]bool(plan.Notify),
	}
	if err := r.Store.SaveTrayAtomic(trayCfg); err != nil {
		return fmt.Errorf("saving tray config: %w", err)
	}

	// 4. Service Management
	binPath, err := r.Finder.Find()
	if err != nil {
		slog.Warn("could not find adder binary for service registration",
			"error", err)
	} else {
		if err := r.Service.RestartIfConfigChanged(binPath, engineCfgPath); err != nil {
			slog.Warn("failed to ensure service is running", "error", err)
		}
	}

	// 5. Connection Update
	r.Conn.SetAddress(trayCfg.APIAddress)
	r.Conn.SetPort(trayCfg.APIPort)

	// Give the service a moment to start
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1 * time.Second):
	}

	if err := r.Conn.Reconnect(); err != nil {
		return fmt.Errorf("service registered but API is unreachable: %w", err)
	}

	return nil
}
