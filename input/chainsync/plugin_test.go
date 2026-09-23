// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chainsync

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testHashA = "0000000000000000000000000000000000000000000000000000000000000001"
	testHashB = "0000000000000000000000000000000000000000000000000000000000000002"
)

func mustPoint(t *testing.T, slot uint64, hash string) ocommon.Point {
	t.Helper()
	hashBytes, err := hex.DecodeString(hash)
	require.NoError(t, err)
	return ocommon.Point{Slot: slot, Hash: hashBytes}
}

func TestConfiguredPluginIntersectPoints(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []ocommon.Point
	}{
		{
			name:  "no whitespace",
			input: "1." + testHashA + ",2." + testHashB,
		},
		{
			name:  "YAML folded scalar (spaces after commas)",
			input: "1." + testHashA + ", 2." + testHashB,
		},
		{
			name:  "YAML literal scalar (newlines and tabs)",
			input: "1." + testHashA + ",\n\t2." + testHashB + "\n",
		},
		{
			// A folded scalar only replaces newlines with spaces for equally
			// indented lines; a more-indented line keeps its newline and
			// leading indent
			name:  "YAML folded scalar (more-indented line)",
			input: "1." + testHashA + ",\n  2." + testHashB,
		},
		{
			name:  "empty entries dropped",
			input: "1." + testHashA + ",,  ,2." + testHashB + ",",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]any{"intersect-point": testCase.input}
			p := mustConfiguredPlugin(t, values)
			require.NotNil(t, p, "plugin should be created")
			c, ok := p.(*ChainSync)
			require.True(t, ok, "plugin should be a *ChainSync")
			assert.Equal(
				t,
				[]ocommon.Point{
					mustPoint(t, 1, testHashA),
					mustPoint(t, 2, testHashB),
				},
				c.intersectPoints,
			)
		})
	}
}

// A single intersect point with leading whitespace must still parse
func TestConfiguredPluginIntersectPointLeadingSpace(t *testing.T) {
	values := map[string]any{"intersect-point": " 1." + testHashA}
	p := mustConfiguredPlugin(t, values)
	require.NotNil(t, p, "plugin should be created")
	c, ok := p.(*ChainSync)
	require.True(t, ok, "plugin should be a *ChainSync")
	assert.Equal(
		t,
		[]ocommon.Point{mustPoint(t, 1, testHashA)},
		c.intersectPoints,
	)
}

// Malformed points are still a hard error, not silently skipped
func TestConfiguredPluginIntersectPointInvalid(t *testing.T) {
	testCases := []struct {
		name  string
		input string
	}{
		{name: "missing hash", input: "1"},
		{name: "non-numeric slot", input: "abc." + testHashA},
		{name: "invalid hex hash", input: "1.zzzz"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string]any{"intersect-point": testCase.input}
			p, err := plugin.GetPlugin(
				plugin.PluginTypeInput,
				"chainsync",
				values,
			)
			require.Error(t, err)
			assert.Nil(t, p)
		})
	}
}

// A value that trims away to nothing must fall back to the intersect-tip
// behaviour, exactly as an unset value does. Treating it as "zero intersect
// points" instead would silently sync from genesis.
func TestConfiguredPluginIntersectPointWhitespaceOnly(t *testing.T) {
	for _, input := range []string{"", "  ,\n ", ","} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			values := map[string]any{"intersect-point": input}
			p := mustConfiguredPlugin(t, values)
			require.NotNil(t, p, "plugin should be created")
			c, ok := p.(*ChainSync)
			require.True(t, ok, "plugin should be a *ChainSync")
			assert.Empty(t, c.intersectPoints)
			assert.True(
				t,
				c.intersectTip,
				"should fall back to intersect-tip, not genesis",
			)
		})
	}
}
