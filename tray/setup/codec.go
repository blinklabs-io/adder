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

	// Clear existing filters
	delete(c.Plugin["filter"]["cardano"], "address")
	delete(c.Plugin["filter"]["cardano"], "drep")
	delete(c.Plugin["filter"]["cardano"], "pool")

	switch p.Filter.Template {
	case "Watch Wallet":
		c.Plugin["filter"]["cardano"]["address"] = p.Filter.Param
	case "Track DRep":
		c.Plugin["filter"]["cardano"]["drep"] = p.Filter.Param
	case "Monitor Pool":
		c.Plugin["filter"]["cardano"]["pool"] = p.Filter.Param
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
	plan := SetupPlan{
		API: APIConfig{
			Address: c.Api.ListenAddress,
			Port:    c.Api.ListenPort,
		},
		App: AppConfig{
			AutoStart: tray.AutoStart,
		},
		Notify: NotificationPrefs(tray.NotifyPrefs),
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

	// Filter
	if cardano, ok := c.Plugin["filter"]["cardano"]; ok {
		if addr, ok := cardano["address"]; ok {
			plan.Filter.Template = "Watch Wallet"
			plan.Filter.Param = fmt.Sprint(addr)
		} else if drep, ok := cardano["drep"]; ok {
			plan.Filter.Template = "Track DRep"
			plan.Filter.Param = fmt.Sprint(drep)
		} else if pool, ok := cardano["pool"]; ok {
			plan.Filter.Template = "Monitor Pool"
			plan.Filter.Param = fmt.Sprint(pool)
		} else {
			plan.Filter.Template = "Monitor Everything"
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
