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
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"text/template"

	"github.com/blinklabs-io/adder/internal/cardanofmt"
)

// lovelaceSentinel is rendered in place of a malformed lovelace amount
// (negative, NaN, etc.). Returning a visible "?" rather than "" so the
// notification body reads "Received ? ADA at addr1..." instead of
// silently swallowing the field, which would let upstream corruption
// pass as a UI glitch and go uninvestigated.
const lovelaceSentinel = "?"

// formatFuncs is the Cardano-aware template.FuncMap registered when the
// engine renders a rule's Title/Body. The helpers accept `any` because
// JSON-decoded event payloads arrive as float64 numbers and string
// addresses/hashes; a stricter signature would cause a template
// execution error (and a silent fall back to the raw template string).
func formatFuncs() template.FuncMap {
	return template.FuncMap{
		"trunc":     truncMiddle,
		"truncHash": truncHash,
		"ada":       lovelaceToAda,
		"outAddr":   firstOutputAddress,
		"outAda":    totalOutputAda,
		"field":     firstItemField,
		"int":       intString,
		// Governance-aware: select the voting procedure cast by a followed
		// DRep so a multi-vote event renders the RIGHT vote, not the first.
		"voteFor": votingProcedureFor,
		// Wallet-aware helpers: distinguish the watched address
		// (".params" in the template data) from the counterparty when
		// rendering tx notifications.
		"mine":     truncMatchingAddress,
		"other":    truncNonMatchingAddress,
		"mineAda":  totalAdaForMatchingAddresses,
		"otherAda": totalAdaForNonMatchingAddresses,
	}
}

// truncMatchingAddress returns the truncated first output address that
// is in addrs (the watched-wallet set), or "" when no output is
// watched. Lets the Incoming template render "Received N ADA at
// <my-address>" instead of picking the first output (which might be
// the sender's change).
func truncMatchingAddress(outputs, addrs any) string {
	set := addrSet(addrs)
	addr, ok := firstOutputAddressWhere(outputs, func(a string) bool {
		_, in := set[a]
		return in
	})
	if !ok {
		return ""
	}
	return cardanofmt.TruncateMiddle(addr, 8, 4, "…")
}

// truncNonMatchingAddress returns the truncated first output address
// that is NOT in addrs — the counterparty. Lets the Outgoing template
// render "Sent N ADA to <recipient>".
func truncNonMatchingAddress(outputs, addrs any) string {
	set := addrSet(addrs)
	addr, ok := firstOutputAddressWhere(outputs, func(a string) bool {
		_, in := set[a]
		return !in
	})
	if !ok {
		return ""
	}
	return cardanofmt.TruncateMiddle(addr, 8, 4, "…")
}

// totalAdaForMatchingAddresses sums the amount across outputs whose
// address is in addrs. Lets the Incoming template render the ADA the
// user actually received instead of the whole tx amount (which
// includes the sender's change).
func totalAdaForMatchingAddresses(outputs, addrs any) string {
	set := addrSet(addrs)
	return sumOutputs(outputs, func(a string) bool {
		_, in := set[a]
		return in
	})
}

// totalAdaForNonMatchingAddresses sums the amount across outputs whose
// address is NOT in addrs (what the user actually sent out, excluding
// change back to themselves).
func totalAdaForNonMatchingAddresses(outputs, addrs any) string {
	set := addrSet(addrs)
	return sumOutputs(outputs, func(a string) bool {
		_, in := set[a]
		return !in
	})
}

// addrSet turns the template's .params value (typically []string but
// JSON-decoded data can arrive as []any) into a lookup set.
func addrSet(addrs any) map[string]struct{} {
	out := map[string]struct{}{}
	switch a := addrs.(type) {
	case []string:
		for _, s := range a {
			out[s] = struct{}{}
		}
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok {
				out[s] = struct{}{}
			}
		}
	}
	return out
}

// firstOutputAddressWhere returns the first output address satisfying
// pick, and whether one was found.
func firstOutputAddressWhere(
	outputs any, pick func(string) bool,
) (string, bool) {
	slice, ok := outputs.([]any)
	if !ok {
		return "", false
	}
	for _, o := range slice {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		addr, _ := m["address"].(string)
		if addr != "" && pick(addr) {
			return addr, true
		}
	}
	return "", false
}

// sumOutputs walks outputs and renders the ADA total for outputs whose
// address satisfies pick. Empty result when no matching output carries
// a numeric amount.
func sumOutputs(outputs any, pick func(string) bool) string {
	slice, ok := outputs.([]any)
	if !ok || len(slice) == 0 {
		return ""
	}
	var total float64
	var summed bool
	for _, o := range slice {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		addr, _ := m["address"].(string)
		if !pick(addr) {
			continue
		}
		if amt, ok := toFloat(m["amount"]); ok {
			total += amt
			summed = true
		}
	}
	if !summed {
		return ""
	}
	return lovelaceToAda(total)
}

// truncHash shortens a hex hash (block/tx) to first-8…three-dot…last-8,
// matching the convention used by Cardano explorers, e.g.
// "84ee913d...255af401". Distinct from truncMiddle (8+1+4 with U+2026)
// because hashes are usually displayed with the longer tail and ASCII
// dots, so a copy-paste from the notification matches what the user
// sees on cardanoscan / cexplorer. Non-string input falls back to
// fmt.Sprintf so a bad field never breaks rendering.
func truncHash(v any) string {
	s, ok := v.(string)
	if !ok {
		if v == nil {
			return ""
		}
		s = fmt.Sprintf("%v", v)
	}
	return cardanofmt.TruncateMiddle(s, 8, 8, "...")
}

// intString formats a numeric value as a plain integer string. Block
// numbers and similar large-integer fields decode from JSON as float64,
// which Go's default template formatter (%v) prints in scientific
// notation past ~1e6 (e.g. 13335000 -> "1.3335e+07"). Use {{int x}} in
// templates whose field is a whole number that must read as a count.
// Non-numeric / fractional input falls back to %v so a bad field never
// breaks rendering.
func intString(v any) string {
	if v == nil {
		return ""
	}
	if f, ok := toFloat(v); ok {
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		// Fractional value where integer was expected — render with
		// no exponent rather than swallowing the precision.
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}

// firstItemField returns the named field of the first object in a
// JSON-decoded slice (e.g. the first vote in payload.votingProcedures),
// or "" when the slice is absent/empty or the field is missing. It lets
// templates read array data without text/template's index builtin,
// which panics on an empty slice (panics fall back to the raw template
// and leak literal braces into the notification).
func firstItemField(field string, slice any) string {
	s, ok := slice.([]any)
	if !ok || len(s) == 0 {
		return ""
	}
	m, ok := s[0].(map[string]any)
	if !ok {
		return ""
	}
	v, ok := m[field]
	if !ok || v == nil {
		return ""
	}
	if str, ok := v.(string); ok {
		return str
	}
	// Numbers decode to float64; render integral values without a
	// trailing ".0" (e.g. govActionIndex 42 -> "42").
	if f, ok := toFloat(v); ok {
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}

// votingProcedureFor returns a single-element slice holding the voting
// procedure cast by a followed DRep (matched on voterId or voterHash
// against params), so `field` renders the RIGHT vote when a governance
// event carries several. Falls back to the original slice when params is
// empty (e.g. Monitor Everything) or nothing matches, preserving the
// first-item behaviour. The []any shape keeps `field` (firstItemField)
// working unchanged.
func votingProcedureFor(procedures any, params any) any {
	s, ok := procedures.([]any)
	if !ok || len(s) == 0 {
		return procedures
	}
	ps, ok := params.([]string)
	if !ok || len(ps) == 0 {
		return procedures
	}
	set := make(map[string]struct{}, len(ps))
	for _, p := range ps {
		set[strings.ToLower(p)] = struct{}{}
	}
	for _, e := range s {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["voterId"].(string)
		if _, ok := set[strings.ToLower(id)]; ok {
			return []any{m}
		}
		h, _ := m["voterHash"].(string)
		if _, ok := set[strings.ToLower(h)]; ok {
			return []any{m}
		}
	}
	return procedures
}

// truncMiddle shortens a long identifier (address, hash, DRep/pool ID)
// to its first 8 and last 4 characters joined by an ellipsis, e.g.
// "addr1qxy…wxyz". Strings short enough to need no truncation are
// returned unchanged. Non-string input is rendered via fmt so a bad
// field never breaks rendering.
func truncMiddle(v any) string {
	s, ok := v.(string)
	if !ok {
		if v == nil {
			return ""
		}
		s = fmt.Sprintf("%v", v)
	}
	// Tray notifications prefer the compact 8+1+4 form with a U+2026
	// ellipsis; output/telegram uses 8+3+8 with "...". Both delegate
	// to cardanofmt.TruncateMiddle so a future change to the rune
	// handling lands once.
	return cardanofmt.TruncateMiddle(s, 8, 4, "…")
}

// lovelaceToAda converts a lovelace amount to ADA with up to 6 decimal
// places, trimming trailing zeros (e.g. 500000000 -> "500",
// 1500000 -> "1.5"). Input arrives as float64 from JSON, but uint64
// and the numeric string forms are also accepted defensively. The
// precision-safe integer conversion is delegated to
// cardanofmt.LovelaceToADA so this and the Telegram output never drift
// on large amounts (>2^53 lovelace).
func lovelaceToAda(v any) string {
	lovelace, ok := toFloat(v)
	if !ok {
		return ""
	}
	// NaN, +Inf, -Inf, and any negative value are all treated as
	// malformed: rendering them silently would let upstream corruption
	// pass as a UI glitch. Emit a visible sentinel and log a warning
	// so operators can correlate the malformed notification with the
	// offending event.
	if lovelace != lovelace || // NaN
		lovelace < 0 ||
		lovelace > 1.8e19 { // overflows uint64
		slog.Warn(
			"notification render: malformed lovelace amount; "+
				"treating as malformed and emitting sentinel",
			"value", lovelace,
		)
		return lovelaceSentinel
	}
	s := cardanofmt.LovelaceToADA(uint64(lovelace))
	// Trim trailing zeros so whole numbers render cleanly.
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// firstOutputAddress returns the truncated address of the first output
// in a transaction payload's "outputs" slice, or "" when absent. It
// guards against a missing/empty slice so an event without outputs never
// panics the template (which would leak the raw template braces).
func firstOutputAddress(outputs any) string {
	m, ok := firstOutput(outputs)
	if !ok {
		return ""
	}
	return truncMiddle(m["address"])
}

// totalOutputAda sums the "amount" (lovelace) across all transaction
// outputs and renders the total in ADA. Returns "" when no outputs
// carry a numeric amount.
func totalOutputAda(outputs any) string {
	slice, ok := outputs.([]any)
	if !ok || len(slice) == 0 {
		return ""
	}
	var total float64
	var summed bool
	for _, o := range slice {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if amt, ok := toFloat(m["amount"]); ok {
			total += amt
			summed = true
		}
	}
	if !summed {
		return ""
	}
	return lovelaceToAda(total)
}

// firstOutput returns the first output object of a JSON-decoded outputs
// slice as a map, reporting whether one was present.
func firstOutput(outputs any) (map[string]any, bool) {
	slice, ok := outputs.([]any)
	if !ok || len(slice) == 0 {
		return nil, false
	}
	m, ok := slice[0].(map[string]any)
	return m, ok
}

// toFloat coerces a JSON-decoded numeric value to float64. JSON numbers
// decode to float64, but uint64/int64 (and their string forms) are
// accepted so the helpers also work on raw Go event structs in tests.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case uint32:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
