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

	// 3. Filter Configuration
	if c.Plugin["filter"] == nil {
		c.Plugin["filter"] = make(map[string]map[any]any)
	}
	if c.Plugin["filter"]["cardano"] == nil {
		c.Plugin["filter"]["cardano"] = make(map[any]any)
	}

	// Clear existing filters: ToEngineConfig is the single source of
	// truth for the cardano-filter knobs, so removed targets must not
	// linger in the persisted config.
	delete(c.Plugin["filter"]["cardano"], "address")
	delete(c.Plugin["filter"]["cardano"], "drep")
	delete(c.Plugin["filter"]["cardano"], "pool")

	// MonitorEverything bypasses per-target filtering entirely. Until
	// the tray subscribes to the firehose and the notifications engine
	// is wired into the dispatch path, the adder cardano filter is the
	// thing that limits the event stream — so we populate one knob per
	// list when MonitorEverything is off. Multi-list combinations get
	// AND-filtered on transaction events in this interim state (see
	// Adder-behavior.md); these writes go away once the engine performs
	// per-rule OR matching.
	if !p.Filter.MonitorEverything {
		if len(p.Filter.Wallets) > 0 {
			c.Plugin["filter"]["cardano"]["address"] = strings.Join(
				p.Filter.Wallets,
				",",
			)
		}
		if len(p.Filter.DReps) > 0 {
			c.Plugin["filter"]["cardano"]["drep"] = strings.Join(
				p.Filter.DReps,
				",",
			)
		}
		if len(p.Filter.Pools) > 0 {
			c.Plugin["filter"]["cardano"]["pool"] = strings.Join(
				p.Filter.Pools,
				",",
			)
		}
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
	// Copy the prefs map rather than aliasing tray.NotifyPrefs:
	// NotificationPrefs is map[string]bool, so a type conversion shares
	// the underlying map header. Aliasing would let any later mutation
	// of plan.Notify silently mutate the caller's TrayConfig.
	notify := make(NotificationPrefs, len(tray.NotifyPrefs))
	for k, v := range tray.NotifyPrefs {
		notify[k] = v
	}
	plan := SetupPlan{
		API: APIConfig{
			Address: c.Api.ListenAddress,
			Port:    c.Api.ListenPort,
		},
		App: AppConfig{
			AutoStart: tray.AutoStart,
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

	// Filter — reverse of ToEngineConfig's write. Each present knob
	// becomes a (possibly comma-joined) list on the plan. Both a
	// missing cardano-filter section AND a present-but-empty one map
	// to MonitorEverything, so an imported config without the filter
	// subtree round-trips cleanly through the wizard instead of
	// leaving the user stuck at step 3.
	cardano, hasCardano := c.Plugin["filter"]["cardano"]
	if hasCardano {
		if addr, ok := cardano["address"]; ok {
			plan.Filter.Wallets = splitCSV(fmt.Sprint(addr))
		}
		if drep, ok := cardano["drep"]; ok {
			plan.Filter.DReps = splitCSV(fmt.Sprint(drep))
		}
		if pool, ok := cardano["pool"]; ok {
			plan.Filter.Pools = splitCSV(fmt.Sprint(pool))
		}
	}
	if len(plan.Filter.Wallets) == 0 &&
		len(plan.Filter.DReps) == 0 &&
		len(plan.Filter.Pools) == 0 {
		plan.Filter.MonitorEverything = true
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
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
