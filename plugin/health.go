// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugin

// ComponentHealth contains only the public identity and observable state of a
// configured pipeline component.
type ComponentHealth struct {
	Role  string
	Index int
	State string
}

// HealthDetails contains diagnostics safe to expose on /healthcheck.
type HealthDetails struct {
	UptimeSeconds   float64
	TerminalFailure bool
	Components      []ComponentHealth
}
