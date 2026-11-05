// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"time"

	"github.com/blinklabs-io/adder/plugin"
)

// HealthDetails reports observable state only. Error strings and plugin
// configuration are deliberately excluded because they may contain secrets.
func (p *Pipeline) HealthDetails() plugin.HealthDetails {
	p.runningMu.RLock()
	running, starting, failed, startedAt := p.running, p.starting, p.failure != nil, p.startedAt
	p.runningMu.RUnlock()
	uptime := float64(0)
	if running && !startedAt.IsZero() {
		uptime = time.Since(startedAt).Seconds()
	}
	components := make(
		[]plugin.ComponentHealth,
		0,
		len(p.inputs)+len(p.filters)+len(p.outputs),
	)
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
				} else if starting {
					state = "starting"
				} else {
					state = "stopped"
				}
			}
			components = append(components, plugin.ComponentHealth{
				Role: group.role, Index: index, State: state,
			})
		}
	}
	return plugin.HealthDetails{
		UptimeSeconds: uptime, TerminalFailure: failed, Components: components,
	}
}
