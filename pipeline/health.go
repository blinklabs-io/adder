// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"time"

	"github.com/blinklabs-io/adder/plugin"
)

// HealthDetails reports observable state only. Error strings and plugin
// configuration are deliberately excluded because they may contain secrets.
func (p *Pipeline) HealthDetails() map[string]any {
	p.runningMu.RLock()
	running, failed, startedAt := p.running, p.failure != nil, p.startedAt
	p.runningMu.RUnlock()
	uptime := float64(0)
	if running && !startedAt.IsZero() {
		uptime = time.Since(startedAt).Seconds()
	}
	components := make([]map[string]any, 0, len(p.inputs)+len(p.filters)+len(p.outputs))
	for _, group := range []struct {
		role    string
		plugins []plugin.ManagedPlugin
	}{
		{"input", p.inputs}, {"filter", p.filters}, {"output", p.outputs},
	} {
		for index, component := range group.plugins {
			state := "unknown"
			if component.Failure() != nil {
				state = "failed"
			} else if reporter, ok := component.(interface{ Running() bool }); ok {
				if reporter.Running() {
					state = "running"
				} else {
					state = "stopped"
				}
			}
			components = append(components, map[string]any{
				"role": group.role, "index": index, "state": state,
			})
		}
	}
	return map[string]any{
		"running": running, "terminalFailure": failed,
		"uptimeSeconds": uptime, "components": components,
	}
}
