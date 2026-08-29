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

	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testHashA = "0000000000000000000000000000000000000000000000000000000000000001"
	testHashB = "0000000000000000000000000000000000000000000000000000000000000002"
)

// setIntersectPoint sets the package-level option for the duration of a test
// and restores the previous value afterwards
func setIntersectPoint(t *testing.T, val string) {
	t.Helper()
	orig := cmdlineOptions.intersectPoint
	t.Cleanup(func() { cmdlineOptions.intersectPoint = orig })
	cmdlineOptions.intersectPoint = val
}

func mustPoint(t *testing.T, slot uint64, hash string) ocommon.Point {
	t.Helper()
	hashBytes, err := hex.DecodeString(hash)
	require.NoError(t, err)
	return ocommon.Point{Slot: slot, Hash: hashBytes}
}

func TestNewFromCmdlineOptionsIntersectPoints(t *testing.T) {
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
			setIntersectPoint(t, testCase.input)
			p := NewFromCmdlineOptions()
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
func TestNewFromCmdlineOptionsIntersectPointLeadingSpace(t *testing.T) {
	setIntersectPoint(t, " 1."+testHashA)
	p := NewFromCmdlineOptions()
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
func TestNewFromCmdlineOptionsIntersectPointInvalid(t *testing.T) {
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
			setIntersectPoint(t, testCase.input)
			assert.Nil(
				t,
				NewFromCmdlineOptions(),
				"invalid intersect point should not yield a plugin",
			)
		})
	}
}

// A value that trims away to nothing must fall back to the intersect-tip
// behaviour, exactly as an unset value does. Treating it as "zero intersect
// points" instead would silently sync from genesis.
func TestNewFromCmdlineOptionsIntersectPointWhitespaceOnly(t *testing.T) {
	origTip := cmdlineOptions.intersectTip
	t.Cleanup(func() { cmdlineOptions.intersectTip = origTip })
	cmdlineOptions.intersectTip = true

	for _, input := range []string{"", "  ,\n ", ","} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			setIntersectPoint(t, input)
			p := NewFromCmdlineOptions()
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
