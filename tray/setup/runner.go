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
	"maps"
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

// ApplyResult carries the soft (non-fatal) errors from Apply so
// callers can surface them to the user. Apply still succeeds (config
// is saved) but the running service may not reflect the change.
type ApplyResult struct {
	// BinaryFindErr is non-nil when Finder.Find failed; the service
	// was NOT (re)started.
	BinaryFindErr error
	// ServiceRestartErr is non-nil when the service manager failed
	// to (re)start adder; the running process may not reflect the
	// new config.
	ServiceRestartErr error
}

// HasSoftErrors reports whether the result carries any non-fatal
// error worth surfacing to the user.
func (r ApplyResult) HasSoftErrors() bool {
	return r.BinaryFindErr != nil || r.ServiceRestartErr != nil
}

func (r *SetupRunner) Apply(
	ctx context.Context, plan SetupPlan,
) (ApplyResult, error) {
	slog.Info("applying setup plan")
	var result ApplyResult

	// 1. Prepare engine config
	engineCfg, err := r.Store.LoadEngine(filepath.Join(ConfigDir(), "config.yaml"))
	if err != nil {
		return result, err
	}
	engineCfg = plan.ToEngineConfig(engineCfg)

	// 2. Save Engine Config
	engineCfgPath := filepath.Join(ConfigDir(), "config.yaml")
	if err := r.Store.SaveEngineAtomic(engineCfgPath, engineCfg); err != nil {
		return result, fmt.Errorf("saving engine config: %w", err)
	}

	// 3. Save Tray Config — Filter lives here, not in engine config
	// (the cardano filter would AND-combine kinds on tx events).
	// Notify map and Filter slices are deep-copied so later mutations
	// of plan don't leak into the persisted TrayConfig.
	notify := make(map[string]bool, len(plan.Notify))
	maps.Copy(notify, plan.Notify)
	trayCfg := TrayConfig{
		APIAddress:       engineCfg.Api.ListenAddress,
		APIPort:          engineCfg.Api.ListenPort,
		AdderConfig:      engineCfgPath,
		AutoStart:        plan.App.AutoStart,
		NotifyPrefs:      notify,
		Filter:           CloneFilter(plan.Filter),
		NotifyRateLimit:  plan.App.NotifyRateLimit,
		NotifyRateWindow: plan.App.NotifyRateWindow,
	}
	if err := r.Store.SaveTrayAtomic(trayCfg); err != nil {
		return result, fmt.Errorf("saving tray config: %w", err)
	}

	// 4. Service Management — Finder/Restart failures are soft and
	// surfaced via ApplyResult; the config is already persisted.
	binPath, err := r.Finder.Find()
	if err != nil {
		result.BinaryFindErr = err
		slog.Error("could not find adder binary for service registration",
			"stage", "binary-find",
			"error", err)
	} else {
		if err := r.Service.RestartIfConfigChanged(binPath, engineCfgPath); err != nil {
			result.ServiceRestartErr = err
			slog.Error("failed to ensure service is running",
				"stage", "service-restart",
				"error", err)
		}
	}

	// 5. Connection Update
	r.Conn.SetAddress(trayCfg.APIAddress)
	r.Conn.SetPort(trayCfg.APIPort)

	// Give the service a moment to start
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-time.After(1 * time.Second):
	}

	if err := r.Conn.Reconnect(); err != nil {
		// Wrap with the soft-error context so the message names the
		// root cause, not just the downstream symptom.
		if result.BinaryFindErr != nil {
			return result, fmt.Errorf(
				"service registered but API is unreachable "+
					"(binary not found: %w): %w",
				result.BinaryFindErr, err)
		}
		if result.ServiceRestartErr != nil {
			return result, fmt.Errorf(
				"service registered but API is unreachable "+
					"(restart failed: %w): %w",
				result.ServiceRestartErr, err)
		}
		return result, fmt.Errorf(
			"service registered but API is unreachable: %w", err)
	}

	return result, nil
}
