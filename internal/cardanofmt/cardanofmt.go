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

// Package cardanofmt holds the small, dependency-free formatting
// helpers that multiple output channels share (tray notifications,
// Telegram output, future log formatters). Putting them here prevents
// the same conversion / truncation logic being re-implemented per
// package with subtly different precision or rune handling, which is
// how cross-channel inconsistency leaks in (a whale ADA amount
// formatted one way in the tray and another way in Telegram for the
// same transaction).
package cardanofmt

import "fmt"

// LovelacePerADA is the conversion factor (1 ADA = 1,000,000 lovelace).
const LovelacePerADA = 1_000_000

// LovelaceToADA converts a lovelace amount to a decimal ADA string with
// 6 decimal places. Uses integer division and remainder so amounts
// above 2^53 lovelace (~9 quadrillion) do not suffer float64 rounding.
// Callers that want trailing zeros trimmed (e.g. "1.5" instead of
// "1.500000") should post-process the result themselves; this helper
// owns the precision-safe conversion only.
func LovelaceToADA(lovelace uint64) string {
	ada := lovelace / LovelacePerADA
	frac := lovelace % LovelacePerADA
	return fmt.Sprintf("%d.%06d", ada, frac)
}

// TruncateMiddle shortens a long identifier (address, hash, DRep/pool
// ID) to head + sep + tail runes, e.g. TruncateMiddle("addr1qxy…wxyz",
// 8, 4, "…") returns "addr1qxy…wxyz". Operates on runes so multibyte
// input is never split mid-rune. Returns s unchanged when it is short
// enough that truncation would only add length.
func TruncateMiddle(s string, head, tail int, sep string) string {
	r := []rune(s)
	if len(r) <= head+tail+len([]rune(sep)) {
		return s
	}
	return string(r[:head]) + sep + string(r[len(r)-tail:])
}
