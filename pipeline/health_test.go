// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"encoding/json"
	"testing"

	"github.com/blinklabs-io/adder/output/webhook"
	"github.com/stretchr/testify/require"
)

func TestHealthDetailsReportObservableState(t *testing.T) {
	p := New()
	p.AddOutput(webhook.New(webhook.WithUrl("https://user:secret@example.com/private", false)))
	require.NoError(t, p.Start())
	details := p.HealthDetails()
	require.Equal(t, true, details["running"])
	require.Equal(t, false, details["terminalFailure"])
	require.GreaterOrEqual(t, details["uptimeSeconds"].(float64), float64(0))
	components := details["components"].([]map[string]any)
	require.Len(t, components, 1)
	require.Equal(t, "running", components[0]["state"])
	encoded, err := json.Marshal(details)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret")
	require.NotContains(t, string(encoded), "example.com")
	require.NoError(t, p.Stop())
	require.Equal(t, false, p.HealthDetails()["running"])
}
