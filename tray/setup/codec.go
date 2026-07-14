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
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/blinklabs-io/adder/internal/config"
)

// ToEngineConfig translates a SetupPlan into an engine-compatible Config object.
func (p SetupPlan) ToEngineConfig(base config.Config) config.Config {
	c := base

	// 1. API Configuration
	c.Api.ListenAddress = p.API.Address
	c.Api.ListenPort = p.API.Port

	// 2. Input Configuration (Chainsync)
	if c.Plugin == nil {
		c.Plugin = make(map[string]map[string]map[any]any)
	}
	if c.Plugin["input"] == nil {
		c.Plugin["input"] = make(map[string]map[any]any)
	}
	if c.Plugin["input"]["chainsync"] == nil {
		c.Plugin["input"]["chainsync"] = make(map[any]any)
	}

	c.Plugin["input"]["chainsync"]["network"] = strings.ToLower(p.Network.Name)
	if p.Network.CustomAddress != "" {
		c.Plugin["input"]["chainsync"]["address"] = fmt.Sprintf(
			"%s:%d",
			p.Network.CustomAddress,
			p.Network.CustomPort,
		)
	} else {
		delete(c.Plugin["input"]["chainsync"], "address")
	}

	// 3. Filter Configuration — the tray owns filter semantics and
	// persists them on TrayConfig, so none of the UI target lists may
	// be written into the sidecar config. Delete any stale sidecar
	// knobs from hand-edited configs or older tray versions.
	if cardano, ok := c.Plugin["filter"]["cardano"]; ok {
		delete(cardano, "address")
		delete(cardano, "drep")
		delete(cardano, "pool")
		delete(cardano, "asset")
		delete(cardano, "policy")
	}

	// 4. Output Configuration
	if c.Plugin["output"] == nil {
		c.Plugin["output"] = make(map[string]map[any]any)
	}

	c.Output = p.Output.Type
	if c.Output == "" || c.Output == "none" {
		c.Output = "log"
		c.Plugin["output"]["log"] = map[any]any{
			"format": "text",
		}
	} else {
		// Replace rather than merge so keys removed/changed during a
		// reconfigure do not linger in the persisted config.
		c.Plugin["output"][c.Output] = make(map[any]any)
		for k, v := range p.Output.Config {
			c.Plugin["output"][c.Output][k] = v
		}
	}

	return c
}

// SetupPlanFromEngineConfig reconstructs a SetupPlan from an existing engine
// configuration and tray settings.
func SetupPlanFromEngineConfig(c config.Config, tray TrayConfig) SetupPlan {
	// Copy (don't alias) so plan mutations don't leak into the
	// caller's TrayConfig — NotificationPrefs is map[string]bool and
	// a type conversion would share the map header.
	notify := make(NotificationPrefs, len(tray.NotifyPrefs))
	maps.Copy(notify, tray.NotifyPrefs)
	plan := SetupPlan{
		API: APIConfig{
			Address: c.Api.ListenAddress,
			Port:    c.Api.ListenPort,
		},
		App: AppConfig{
			AutoStart:        tray.AutoStart,
			NotifyRateLimit:  tray.NotifyRateLimit,
			NotifyRateWindow: tray.NotifyRateWindow,
		},
		Notify: notify,
	}

	// Network
	if cs, ok := c.Plugin["input"]["chainsync"]; ok {
		if net, ok := cs["network"]; ok {
			plan.Network.Name = fmt.Sprint(net)
		}
		if addr, ok := cs["address"]; ok {
			s := fmt.Sprint(addr)
			if idx := strings.LastIndex(s, ":"); idx != -1 {
				plan.Network.CustomAddress = s[:idx]
				if p, err := strconv.Atoi(s[idx+1:]); err == nil {
					plan.Network.CustomPort = uint(p)
				}
			}
		}
	}

	// Filter lives on the tray config so the tray notification engine
	// owns target matching semantics. Empty tray config →
	// MonitorEverything so the wizard advances. CloneFilter detaches
	// the slices so plan mutations don't leak into tray.Filter.
	plan.Filter = CloneFilter(tray.Filter)
	if !plan.Filter.MonitorEverything &&
		len(plan.Filter.Wallets) == 0 &&
		len(plan.Filter.DReps) == 0 &&
		len(plan.Filter.Pools) == 0 &&
		len(plan.Filter.Assets) == 0 &&
		len(plan.Filter.Policies) == 0 {
		plan.Filter.MonitorEverything = true
	}

	// Legacy migration: if the tray config has no Filter but the
	// engine config still carries filter.cardano knobs (older tray
	// versions, hand-edited configs, CLI usage), pull them in once
	// so the upgrade does not lose configured targets.
	if !tray.Filter.MonitorEverything &&
		len(tray.Filter.Wallets) == 0 &&
		len(tray.Filter.DReps) == 0 &&
		len(tray.Filter.Pools) == 0 &&
		len(tray.Filter.Assets) == 0 &&
		len(tray.Filter.Policies) == 0 {
		if cardano, ok := c.Plugin["filter"]["cardano"]; ok {
			if addr, ok := cardano["address"]; ok {
				plan.Filter.Wallets = splitCSV(fmt.Sprint(addr))
			}
			if drep, ok := cardano["drep"]; ok {
				plan.Filter.DReps = splitCSV(fmt.Sprint(drep))
			}
			if pool, ok := cardano["pool"]; ok {
				plan.Filter.Pools = splitCSV(fmt.Sprint(pool))
			}
			if asset, ok := cardano["asset"]; ok {
				plan.Filter.Assets = splitCSV(fmt.Sprint(asset))
			}
			if policy, ok := cardano["policy"]; ok {
				plan.Filter.Policies = splitCSV(fmt.Sprint(policy))
			}
			if len(plan.Filter.Wallets) > 0 ||
				len(plan.Filter.DReps) > 0 ||
				len(plan.Filter.Pools) > 0 ||
				len(plan.Filter.Assets) > 0 ||
				len(plan.Filter.Policies) > 0 {
				plan.Filter.MonitorEverything = false
			}
		}
	}

	// Output
	plan.Output.Type = c.Output
	plan.Output.Config = make(map[string]string)
	if c.Output != "log" && c.Output != "" {
		if cfg, ok := c.Plugin["output"][c.Output]; ok {
			for k, v := range cfg {
				plan.Output.Config[fmt.Sprint(k)] = fmt.Sprint(v)
			}
		}
	} else {
		// Engine "log" without path maps to UI "none"
		if cfg, ok := c.Plugin["output"]["log"]; ok {
			if path, ok := cfg["path"]; ok && fmt.Sprint(path) != "" {
				plan.Output.Type = "log"
				plan.Output.Config["path"] = fmt.Sprint(path)
			} else {
				plan.Output.Type = "none"
			}
		} else {
			plan.Output.Type = "none"
		}
	}

	return plan
}

// splitCSV splits a comma-separated string into trimmed, non-empty
// entries — the inverse of strings.Join(list, ",") used by
// ToEngineConfig when persisting each cardano-filter knob.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
