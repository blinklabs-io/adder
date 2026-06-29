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

package wizard

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// targetSection is the editable list of one kind of monitoring target
// (wallets, DReps, pools, assets, or policies). Owns its entry+Add
// row, entries list, and per-section validation label. Consumed by
// the wizard's templateStep and the standalone RulesEditor; per-
// surface UX (e.g. confirm-before-delete) is injected via callbacks.
type targetSection struct {
	label       string // "Wallets" / "DReps" / "Pools" / ...
	placeholder string
	validate    func(string) error // ValidateWalletAddr etc.

	entry    *widget.Entry
	addBtn   *widget.Button
	errLabel *widget.Label
	list     *fyne.Container // VBox of entry rows
	values   []string
	rowBtns  []*widget.Button // trash buttons parallel to values

	// onChange is invoked after every add/remove so the parent surface
	// can update derived UI (summary line, rule list rebuild).
	onChange func()

	// onDelete, when non-nil, wraps every row's trash-button activation.
	// The wrapper receives the row's value and a doDelete continuation
	// it must invoke to actually remove the row. Consumers that want a
	// confirm dialog implement that here; the default (nil) behaves as
	// immediate delete.
	onDelete func(v string, doDelete func())
}

// newTargetSection wires up one editable target list. The Add button
// validates the input via `validate`, surfaces the error inline on
// failure, and appends a labelled row with a trash-icon remove button
// on success. onChange is called after every successful add or remove.
func newTargetSection(
	label, placeholder string,
	validate func(string) error,
	onChange func(),
) *targetSection {
	sec := &targetSection{
		label:       label,
		placeholder: placeholder,
		validate:    validate,
		onChange:    onChange,
	}
	sec.entry = widget.NewEntry()
	sec.entry.SetPlaceHolder(placeholder)
	sec.errLabel = widget.NewLabel("")
	sec.errLabel.Wrapping = fyne.TextWrapWord
	sec.errLabel.Hide()
	sec.list = container.NewVBox()
	sec.addBtn = widget.NewButtonWithIcon(
		"Add",
		theme.ContentAddIcon(),
		func() { sec.add(sec.entry.Text) },
	)
	sec.entry.OnSubmitted = func(string) { sec.add(sec.entry.Text) }
	return sec
}

// add validates v (splitting on commas first so pasted CSV produces
// multiple rows) and appends each piece. All-or-nothing: if any piece
// fails validation, nothing is added and the input is preserved.
// Duplicates (against existing values OR within the same batch) are
// rejected as a single error so values stays unique and removeValue's
// "find first match" loop cannot desync the values slice from the
// visible list.
func (sec *targetSection) add(v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		sec.errLabel.SetText("Please enter a value before adding.")
		sec.errLabel.Show()
		return
	}
	parts := splitAndTrim(v)
	if len(parts) == 0 {
		sec.errLabel.SetText("Please enter a value before adding.")
		sec.errLabel.Show()
		return
	}
	for _, p := range parts {
		if err := sec.validate(p); err != nil {
			sec.errLabel.SetText(err.Error())
			sec.errLabel.Show()
			return
		}
	}
	// Dedup case-insensitively: hex identifiers (drep/pool/policy/
	// asset) round-trip through hex.DecodeString which accepts mixed
	// case, so "ABCDEF" and "abcdef" name the same on-chain entity
	// and must not both land in the rules engine. Bech32 entries are
	// spec-mandated lowercase, so case-folding is a no-op for them.
	existing := make(map[string]struct{}, len(sec.values)+len(parts))
	for _, x := range sec.values {
		existing[strings.ToLower(x)] = struct{}{}
	}
	for _, p := range parts {
		k := strings.ToLower(p)
		if _, dup := existing[k]; dup {
			sec.errLabel.SetText(
				"\"" + p + "\" is already in the " + sec.label +
					" list.")
			sec.errLabel.Show()
			return
		}
		existing[k] = struct{}{}
	}
	sec.errLabel.Hide()
	for _, p := range parts {
		sec.appendRow(p)
	}
	sec.entry.SetText("")
	if sec.onChange != nil {
		sec.onChange()
	}
}

// appendRow grows values + rowBtns + list as a single unit so the
// parallel slices and the visible container never drift.
func (sec *targetSection) appendRow(v string) {
	rowObj, btn := sec.row(v)
	sec.values = append(sec.values, v)
	sec.rowBtns = append(sec.rowBtns, btn)
	sec.list.Add(rowObj)
}

// splitAndTrim splits s on commas, trims whitespace from each piece,
// and drops empties. A trailing comma or accidental "addr1a, ,addr1b"
// never produces an empty entry that would fail validation with a
// confusing "must not be empty" error.
func splitAndTrim(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// row builds a single entry row and returns it together with its trash
// button so callers (appendRow / setValues) can index the button into
// rowBtns. Trash activation runs through onDelete when set (wrapper
// may show a confirm dialog) or removes immediately when nil.
func (sec *targetSection) row(v string) (fyne.CanvasObject, *widget.Button) {
	lbl := widget.NewLabel(v)
	lbl.Wrapping = fyne.TextWrapBreak
	var rowObj *fyne.Container
	removeBtn := widget.NewButtonWithIcon(
		"",
		theme.DeleteIcon(),
		func() {
			doDelete := func() { sec.removeValue(v, rowObj) }
			if sec.onDelete != nil {
				sec.onDelete(v, doDelete)
				return
			}
			doDelete()
		},
	)
	rowObj = container.NewBorder(
		nil, nil, nil, removeBtn, lbl,
	)
	return rowObj, removeBtn
}

func (sec *targetSection) removeValue(v string, row fyne.CanvasObject) {
	for i, x := range sec.values {
		if x == v {
			sec.values = append(sec.values[:i], sec.values[i+1:]...)
			sec.rowBtns = append(sec.rowBtns[:i], sec.rowBtns[i+1:]...)
			break
		}
	}
	sec.list.Remove(row)
	if sec.onChange != nil {
		sec.onChange()
	}
}

// setValues replaces the section's contents with vs, dropping
// case-insensitive duplicates to match add()'s uniqueness invariant.
func (sec *targetSection) setValues(vs []string) {
	sec.values = sec.values[:0]
	sec.rowBtns = sec.rowBtns[:0]
	sec.list.RemoveAll()
	// Case-insensitive dedup, matching add()'s invariant.
	seen := make(map[string]struct{}, len(vs))
	for _, v := range vs {
		k := strings.ToLower(v)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		sec.appendRow(v)
	}
}

// setEnabled toggles every input the user can interact with on this
// section — the entry, the Add button, AND every per-row trash button.
// Centralises the freeze/thaw policy so a new dynamic widget added to
// a row is one place to track instead of two.
func (sec *targetSection) setEnabled(enabled bool) {
	if enabled {
		sec.entry.Enable()
		sec.addBtn.Enable()
		for _, b := range sec.rowBtns {
			b.Enable()
		}
		return
	}
	sec.entry.Disable()
	sec.addBtn.Disable()
	for _, b := range sec.rowBtns {
		b.Disable()
	}
}

func (sec *targetSection) canvasObject() fyne.CanvasObject {
	return container.NewVBox(
		widget.NewLabelWithStyle(
			sec.label, fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		container.NewBorder(
			nil, nil, nil, sec.addBtn, sec.entry,
		),
		sec.errLabel,
		sec.list,
	)
}
