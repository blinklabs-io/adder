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

package cardanofmt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLovelaceToADA(t *testing.T) {
	cases := []struct {
		name     string
		lovelace uint64
		want     string
	}{
		{"zero", 0, "0.000000"},
		{"one ADA", 1_000_000, "1.000000"},
		{"fractional", 1_500_000, "1.500000"},
		{"sub-ADA", 1, "0.000001"},
		{
			// Above 2^53 — float64 would lose precision here;
			// the integer-division path keeps the last digit.
			name:     "above 2^53",
			lovelace: 1<<53 + 7,
			want:     "9007199254.740999",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, LovelaceToADA(tc.lovelace))
		})
	}
}

func TestTruncateMiddle(t *testing.T) {
	cases := []struct {
		name           string
		s              string
		head, tail     int
		sep            string
		want           string
	}{
		{
			name: "short input untouched",
			s:    "addr1abc", head: 8, tail: 4, sep: "…",
			want: "addr1abc",
		},
		{
			name: "ellipsis truncation",
			s:    "addr1qxy0123456789wxyz", head: 8, tail: 4,
			sep:  "…",
			want: "addr1qxy…wxyz",
		},
		{
			// 22-char input, 8+3+8 → 8 head + "..." + 8 tail.
			name: "dots separator",
			s:    "addr1qxy0123456789wxyz", head: 8, tail: 8,
			sep:  "...",
			want: "addr1qxy...6789wxyz",
		},
		{
			// 21-rune Greek input, head=3 + tail=2 + sep=1 = 6
			// runes; multibyte chars must not be split mid-rune.
			name: "multibyte input not split mid-rune",
			s:    "αβγδεζηθικλμνξοπρστυφχ", head: 3, tail: 2,
			sep:  "…",
			want: "αβγ…φχ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want,
				TruncateMiddle(tc.s, tc.head, tc.tail, tc.sep))
		})
	}
}
