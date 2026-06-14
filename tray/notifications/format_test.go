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

package notifications

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncMiddle(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"long address", "addr1qxy0123456789wxyz", "addr1qxy…wxyz"},
		{"exactly head+tail+1 unchanged", "0123456789abc", "0123456789abc"},
		{"short string unchanged", "addr1", "addr1"},
		{"empty string", "", ""},
		{"nil", nil, ""},
		{
			"multibyte not split",
			"日本語テスト1234567890abcd",
			"日本語テスト12…abcd",
		},
		{
			"non-string coerced then truncated",
			1234567890123456,
			"12345678…3456",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, truncMiddle(tt.in))
		})
	}
}

// TestTruncHash pins the explorer-shape hash format (head 8 + "..." +
// tail 8) used by block-minted notifications so a copy from the
// notification matches what the user sees on cardanoscan / cexplorer.
func TestTruncHash(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			"long hash",
			"84ee913d2d3aaaaabbbb255af401",
			"84ee913d...255af401",
		},
		{"short hash unchanged", "abc", "abc"},
		{"empty", "", ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, truncHash(tt.in))
		})
	}
}

func TestLovelaceToAda(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"whole ada from float64", float64(500_000_000), "500"},
		{"one and a half ada", float64(1_500_000), "1.5"},
		{"sub-ada precision", float64(1), "0.000001"},
		{"zero", float64(0), "0"},
		{"uint64 amount", uint64(2_000_000), "2"},
		{"trims trailing zeros", float64(1_230_000), "1.23"},
		{"numeric string", "3000000", "3"},
		{"non-numeric returns empty", "notanumber", ""},
		{"nil returns empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, lovelaceToAda(tt.in))
		})
	}
}

func TestFirstOutputAddress(t *testing.T) {
	outputs := []any{
		map[string]any{"address": "addr1qxy0123456789wxyz", "amount": 1.0},
		map[string]any{"address": "addr1other", "amount": 2.0},
	}
	assert.Equal(t, "addr1qxy…wxyz", firstOutputAddress(outputs))
	// Missing / empty / wrong-typed inputs never panic and yield "".
	assert.Equal(t, "", firstOutputAddress(nil))
	assert.Equal(t, "", firstOutputAddress([]any{}))
	assert.Equal(t, "", firstOutputAddress("not a slice"))
}

func TestTotalOutputAda(t *testing.T) {
	outputs := []any{
		map[string]any{"address": "a", "amount": float64(500_000_000)},
		map[string]any{"address": "b", "amount": float64(250_000_000)},
	}
	assert.Equal(t, "750", totalOutputAda(outputs))
	assert.Equal(t, "", totalOutputAda(nil))
	assert.Equal(t, "", totalOutputAda([]any{}))
	// Outputs without a numeric amount sum to nothing.
	assert.Equal(t, "", totalOutputAda([]any{map[string]any{"address": "a"}}))
}

func TestFirstItemField(t *testing.T) {
	votes := []any{
		map[string]any{
			"voterId":        "drep1abc0123456789wxyz",
			"vote":           "Yes",
			"govActionIndex": float64(42),
		},
	}
	assert.Equal(t, "drep1abc0123456789wxyz", firstItemField("voterId", votes))
	assert.Equal(t, "Yes", firstItemField("vote", votes))
	assert.Equal(t, "42", firstItemField("govActionIndex", votes))
	// Absent slice, empty slice, and missing field all yield "".
	assert.Equal(t, "", firstItemField("voterId", nil))
	assert.Equal(t, "", firstItemField("voterId", []any{}))
	assert.Equal(t, "", firstItemField("missing", votes))
}

// TestLovelaceToAdaRejectsMalformedWithSentinel guards the review
// finding that lovelaceToAda silently returned "" on negative input,
// which collapses the amount field in the rendered notification and
// hides upstream corruption. The sentinel "?" surfaces the malformed
// value visibly while keeping the rest of the notification readable.
func TestLovelaceToAdaRejectsMalformedWithSentinel(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"negative float", float64(-1_000_000), lovelaceSentinel},
		{"negative int", -1, lovelaceSentinel},
		{
			name: "NaN", in: math.NaN(),
			want: lovelaceSentinel,
		},
		{
			// +Inf would overflow the uint64 conversion and wrap.
			name: "positive infinity", in: math.Inf(1),
			want: lovelaceSentinel,
		},
		{
			// 2^64 (above uint64 max) — wraps if not guarded.
			name: "above uint64 max", in: 2e19,
			want: lovelaceSentinel,
		},
		{
			// Valid happy path: 500 ADA, trimmed.
			name: "valid one ADA", in: float64(1_000_000),
			want: "1",
		},
		{
			name: "valid fractional", in: float64(1_500_000),
			want: "1.5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, lovelaceToAda(tc.in))
		})
	}
}

// TestIntString guards the block-number rendering bug where a large
// float64 (JSON-decoded block height) printed via Go's default %v
// formatter came out as scientific notation like "1.3335e+07". The
// `int` template helper must render integral values as plain digits.
func TestIntString(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"large float (block height)", float64(13_335_000), "13335000"},
		{"small float", float64(42), "42"},
		{"int", 7, "7"},
		{"int64", int64(1_234_567), "1234567"},
		{"uint64", uint64(9_876_543), "9876543"},
		{"numeric string", "12345", "12345"},
		{"fractional", float64(1.5), "1.5"},
		{"nil", nil, ""},
		{"non-numeric", true, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, intString(tc.in))
		})
	}
}
