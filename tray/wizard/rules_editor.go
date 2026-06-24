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
	"context"
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
)

// RulesEditor is the post-setup notification rule editor: targets +
// per-category prefs in one window, mutating a working copy that
// Cancel discards and Apply hands to the callback. Shares
// targetSection with the wizard's step 3 but sets onDelete to a
// confirm dialog (the wizard keeps immediate-delete).
type RulesEditor struct {
	window fyne.Window

	// working is the mutated copy of the plan; commit happens only on
	// Apply via the callback. The original plan stays untouched so
	// Cancel is a true no-op.
	working  setup.SetupPlan
	callback func(context.Context, setup.SetupPlan, *RulesEditor)

	ctx    context.Context
	cancel context.CancelFunc

	everythingCheck *widget.Check
	targetsBox      *fyne.Container
	sections        []*targetSection
	wallets         *targetSection
	dreps           *targetSection
	pools           *targetSection
	assets          *targetSection
	policies        *targetSection

	prefChecks map[string]*widget.Check

	applyBtn *widget.Button
	closeBtn *widget.Button

	// applying is set true between onApply firing the callback and the
	// caller releasing the editor (via EnableButtons on a soft / hard
	// failure, or Close on success). While true the Cancel button and
	// the window-close affordance are blocked so the user cannot tear
	// down the warning dialog's parent mid-apply.
	applying bool
}

// NewRulesEditor builds the rule-editor window for the given plan. The
// callback is invoked on Apply with the mutated plan; it is responsible
// for persistence/restart (see App.onRulesApply).
func NewRulesEditor(
	plan setup.SetupPlan,
	callback func(context.Context, setup.SetupPlan, *RulesEditor),
) *RulesEditor {
	a := fyne.CurrentApp()
	if a == nil {
		a = app.New()
	}
	ctx, cancel := context.WithCancel(context.Background())

	e := &RulesEditor{
		working:    setup.ClonePlan(plan),
		callback:   callback,
		ctx:        ctx,
		cancel:     cancel,
		prefChecks: make(map[string]*widget.Check),
	}

	e.window = a.NewWindow("Adder Notification Rules")
	e.window.Resize(fyne.NewSize(560, 640))
	e.window.SetOnClosed(cancel)
	// Block the native window-close affordance while apply is in
	// flight so the warning dialog's parent cannot vanish under it.
	e.window.SetCloseIntercept(func() {
		if e.applying {
			return
		}
		e.Close()
	})

	e.buildTargetSections()
	prefsBox := e.buildPrefBox()

	e.applyBtn = widget.NewButtonWithIcon(
		"Apply & Restart",
		theme.ConfirmIcon(),
		e.onApply,
	)
	e.applyBtn.Importance = widget.HighImportance
	e.closeBtn = widget.NewButton("Cancel", e.Close)

	header := container.NewVBox(
		widget.NewLabelWithStyle(
			"Notification Rules",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		widget.NewLabel(
			"Add or remove monitoring targets and toggle which "+
				"event categories trigger desktop notifications. "+
				"Apply writes the config and restarts Adder.",
		),
	)
	footer := container.NewHBox(
		layout.NewSpacer(),
		e.closeBtn,
		e.applyBtn,
	)

	body := container.NewVBox(
		widget.NewLabelWithStyle(
			"Monitoring Targets",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		e.everythingCheck,
		e.targetsBox,
		widget.NewSeparator(),
		prefsBox,
	)

	e.window.SetContent(container.NewBorder(
		header,
		footer,
		nil,
		nil,
		container.NewVScroll(body),
	))
	return e
}

// ShowRulesEditor launches the notification rule editor window.
func ShowRulesEditor(
	plan setup.SetupPlan,
	callback func(context.Context, setup.SetupPlan, *RulesEditor),
) {
	slog.Debug("launching notification rules editor")
	fyne.Do(func() {
		e := NewRulesEditor(plan, callback)
		e.window.CenterOnScreen()
		e.window.Show()
	})
}

// confirmDelete is the editor's onDelete policy: gate each row's
// trash-button activation behind a confirm dialog rooted at the
// editor window. sectionLabel names the right list in the dialog
// title and body.
func (e *RulesEditor) confirmDelete(sectionLabel string) func(string, func()) {
	return func(v string, doDelete func()) {
		dialog.ShowConfirm(
			"Delete "+sectionLabel,
			"Remove \""+v+"\" from "+sectionLabel+"?",
			func(ok bool) {
				if !ok {
					return
				}
				doDelete()
			},
			e.window,
		)
	}
}

// buildTargetSections constructs the five reused targetSection widgets
// and wires their onDelete to a confirm dialog so each row trash button
// confirms before removing (vs. the wizard's immediate delete).
// onChange refreshes the working filter snapshot.
func (e *RulesEditor) buildTargetSections() {
	mk := func(
		label, placeholder string,
		validate func(string) error,
	) *targetSection {
		sec := newTargetSection(label, placeholder, validate,
			e.snapshotFilter)
		sec.onDelete = e.confirmDelete(label)
		return sec
	}
	e.wallets = mk("Wallets",
		"Cardano address (addr1... or stake1...)",
		setup.ValidateWalletAddr)
	e.dreps = mk("DReps",
		"DRep ID (drep1... or hex)",
		setup.ValidateDRepID)
	e.pools = mk("Pools",
		"Pool ID (pool1... or hex)",
		setup.ValidatePoolID)
	e.assets = mk("Assets",
		"Asset fingerprint (asset1... — CIP-14)",
		setup.ValidateAssetFingerprint)
	e.policies = mk("Policies",
		"Policy ID (56-character hex)",
		setup.ValidatePolicyID)
	e.sections = []*targetSection{
		e.wallets, e.dreps, e.pools, e.assets, e.policies,
	}

	e.wallets.setValues(e.working.Filter.Wallets)
	e.dreps.setValues(e.working.Filter.DReps)
	e.pools.setValues(e.working.Filter.Pools)
	e.assets.setValues(e.working.Filter.Assets)
	e.policies.setValues(e.working.Filter.Policies)

	e.targetsBox = container.NewVBox(
		e.wallets.canvasObject(),
		widget.NewSeparator(),
		e.dreps.canvasObject(),
		widget.NewSeparator(),
		e.pools.canvasObject(),
		widget.NewSeparator(),
		e.assets.canvasObject(),
		widget.NewSeparator(),
		e.policies.canvasObject(),
	)

	// Hydrate Checked + initial visibility BEFORE wiring OnChanged so
	// hydration cannot trigger a spurious user-action side effect.
	// Same pattern as the pref-checks below.
	e.everythingCheck = widget.NewCheck(
		"Monitor Everything (ignore per-target lists)", nil,
	)
	e.everythingCheck.Checked = e.working.Filter.MonitorEverything
	if e.working.Filter.MonitorEverything {
		e.targetsBox.Hide()
	}
	e.everythingCheck.OnChanged = func(checked bool) {
		e.working.Filter.MonitorEverything = checked
		if checked {
			e.targetsBox.Hide()
		} else {
			e.targetsBox.Show()
		}
	}
}

// buildPrefBox builds one widget.Check per pref in setup.AllNotifyPrefs.
// Toggling a check flips that pref on the working plan; the engine
// derives one rule per category per target so flipping a pref
// enables/disables every rule for that category at once.
func (e *RulesEditor) buildPrefBox() fyne.CanvasObject {
	prefs := setup.AllNotifyPrefs()
	rows := make([]fyne.CanvasObject, 0, 2+len(prefs))
	rows = append(
		rows,
		widget.NewLabelWithStyle(
			"Notification Preferences",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		widget.NewLabel(
			"Each preference controls one category of rule across "+
				"all configured targets.",
		),
	)
	if e.working.Notify == nil {
		e.working.Notify = make(setup.NotificationPrefs)
	}
	// Backfill missing prefs with the default-on convention used by
	// wizard step 4 (createChecks), so sparse / upgraded configs do
	// not silently appear opted-out in the editor and an unconfigured
	// pref ships through Apply with the same value the wizard would.
	for _, pref := range prefs {
		if _, ok := e.working.Notify[pref]; !ok {
			e.working.Notify[pref] = true
		}
	}
	for _, pref := range prefs {
		check := widget.NewCheck(pref, nil)
		// Set the initial state BEFORE wiring OnChanged so the
		// hydration assignment cannot trigger a working-map write
		// under any driver that fires OnChanged from SetChecked.
		check.Checked = e.working.Notify[pref]
		check.OnChanged = func(checked bool) {
			e.working.Notify[pref] = checked
		}
		e.prefChecks[pref] = check
		rows = append(rows, check)
	}
	return container.NewVBox(rows...)
}

// snapshotFilter copies the targetSection values back into the working
// filter after every add/remove so Apply ships an up-to-date plan.
// When MonitorEverything is on, the per-target lists are nilled so the
// persisted YAML matches the wizard step3 behaviour and the editor's
// section rows do not "resurrect" on next open.
func (e *RulesEditor) snapshotFilter() {
	if e.working.Filter.MonitorEverything {
		e.working.Filter.Wallets = nil
		e.working.Filter.DReps = nil
		e.working.Filter.Pools = nil
		e.working.Filter.Assets = nil
		e.working.Filter.Policies = nil
		return
	}
	e.working.Filter.Wallets = append(
		[]string(nil), e.wallets.values...,
	)
	e.working.Filter.DReps = append(
		[]string(nil), e.dreps.values...,
	)
	e.working.Filter.Pools = append(
		[]string(nil), e.pools.values...,
	)
	e.working.Filter.Assets = append(
		[]string(nil), e.assets.values...,
	)
	e.working.Filter.Policies = append(
		[]string(nil), e.policies.values...,
	)
}

// onApply snapshots the working plan, freezes every mutating input
// (including Cancel / window-close so the warning dialog's parent
// cannot vanish), and hands a deep copy to the callback (which runs
// on a background goroutine — the deep copy keeps RulesFromPlan reads
// race-free).
func (e *RulesEditor) onApply() {
	e.snapshotFilter()
	e.applying = true
	e.setInputsEnabled(false)
	if e.callback != nil {
		e.callback(e.ctx, setup.ClonePlan(e.working), e)
	}
}

// setInputsEnabled toggles every mutating input on the editor (apply
// button, prefs, everything toggle, section entries + add buttons +
// per-row trash buttons).
func (e *RulesEditor) setInputsEnabled(enabled bool) {
	toggle := func(d fyne.Disableable) {
		if enabled {
			d.Enable()
		} else {
			d.Disable()
		}
	}
	toggle(e.applyBtn)
	toggle(e.closeBtn)
	toggle(e.everythingCheck)
	for _, c := range e.prefChecks {
		toggle(c)
	}
	for _, sec := range e.sections {
		sec.setEnabled(enabled)
	}
}

// Window returns the editor's top-level window so external callers
// can parent dialogs on the surface that initiated the work.
func (e *RulesEditor) Window() fyne.Window {
	return e.window
}

// Close cancels the editor context and closes the window. Marks
// applying=false first so onRulesApply's success path can call Close
// even when the window is still showing the warning dialog.
func (e *RulesEditor) Close() {
	e.applying = false
	e.cancel()
	e.window.Close()
}

// EnableButtons re-enables every input frozen by onApply, restoring
// the Cancel button + native window-close affordance.
func (e *RulesEditor) EnableButtons() {
	e.applying = false
	e.setInputsEnabled(true)
}
