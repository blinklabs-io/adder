// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package event

import (
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

func TestCompiledTypeSelection(t *testing.T) {
	types := []string{event.TypeBlock, event.TypeRollback}
	matcher, err := Compile(types)
	require.NoError(t, err)
	types[0] = event.TypeTransaction
	for _, tc := range []struct {
		eventType string
		want      bool
	}{
		{event.TypeBlock, true},
		{event.TypeRollback, true},
		{event.TypeTransaction, false},
		{"custom", false},
		{"", false},
	} {
		require.Equal(t, tc.want, matcher.Match(event.Event{Type: tc.eventType}))
	}
	for _, invalid := range [][]string{nil, {}, {"block"}, {event.TypeBlock, "custom"}} {
		_, err := Compile(invalid)
		require.Error(t, err)
	}
}

func TestLegacyTypeSelectionRemainsPermissive(t *testing.T) {
	require.True(t, newMatcher(nil).Match(event.Event{Type: "custom"}))
	require.True(t, newMatcher([]string{"custom"}).Match(event.Event{Type: "custom"}))
	require.False(t, newMatcher([]string{"custom"}).Match(event.Event{Type: event.TypeBlock}))
}
