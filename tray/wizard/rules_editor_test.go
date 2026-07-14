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
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func samplePlan() setup.SetupPlan {
	return setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{
				"addr1q9hlrf6lmtgu7mqupwlysw7qcvexmjkmwfynqlfh33dz87xy3y67g60shkajwfsewt2tjs85a3xkpkmcafpwwzpevlcsmwzj82",
			},
			DReps: []string{
				"drep1y2cvruq6syfa4w7uksw9jur9q06lwlc60p9kjcxnxd9ww7gh8gtt0",
			},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:       true,
			setup.NotifyPrefOutgoingTx:       false,
			setup.NotifyPrefBlocksMinted:     true,
			setup.NotifyPrefConnectionIssues: true,
		},
		Output: setup.OutputConfig{Config: map[string]string{}},
	}
}

func TestSetupClonePlanIsDeep(t *testing.T) {
	orig := samplePlan()
	clone := setup.ClonePlan(orig)

	clone.Filter.Wallets = append(clone.Filter.Wallets, "addr1other")
	clone.Notify[setup.NotifyPrefIncomingTx] = false
	clone.Output.Config["x"] = "y"

	// Original maps + slices must be untouched.
	assert.Len(t, orig.Filter.Wallets, 1)
	assert.True(t, orig.Notify[setup.NotifyPrefIncomingTx])
	_, ok := orig.Output.Config["x"]
	assert.False(t, ok)
}

func TestNewRulesEditorHydratesFromPlan(t *testing.T) {
	test.NewApp()
	called := false
	e := NewRulesEditor(samplePlan(), func(
		_ context.Context,
		_ setup.SetupPlan,
		_ *RulesEditor,
	) {
		called = true
	})
	require.NotNil(t, e.window)
	// Target sections hydrated.
	assert.Equal(t, []string{
		"addr1q9hlrf6lmtgu7mqupwlysw7qcvexmjkmwfynqlfh33dz87xy3y67g60shkajwfsewt2tjs85a3xkpkmcafpwwzpevlcsmwzj82",
	}, e.wallets.values)
	assert.Equal(t,
		[]string{"drep1y2cvruq6syfa4w7uksw9jur9q06lwlc60p9kjcxnxd9ww7gh8gtt0"},
		e.dreps.values)
	assert.Empty(t, e.pools.values)
	// Pref checkboxes hydrated.
	assert.True(t, prefCheck(t, e, setup.NotifyPrefIncomingTx).Checked)
	assert.False(t, prefCheck(t, e, setup.NotifyPrefOutgoingTx).Checked)
	assert.True(t, prefCheck(t, e, setup.NotifyPrefBlocksMinted).Checked)
	// MonitorEverything toggle off.
	assert.False(t, e.everythingCheck.Checked)

	e.onApply()
	assert.True(t, called)

	e.Close()
	assert.ErrorIs(t, e.ctx.Err(), context.Canceled)
}

func TestRulesEditorPrefToggleFlipsWorking(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)

	// Flip Outgoing from false -> true via the check's OnChanged
	// (what a user tap invokes).
	chk := prefCheck(t, e, setup.NotifyPrefOutgoingTx)
	require.False(t, chk.Checked)
	chk.OnChanged(true)
	assert.True(t, e.working.Notify[setup.NotifyPrefOutgoingTx])

	// Same-value call is observationally a no-op (assignment writes
	// true over true).
	chk.OnChanged(true)
	assert.True(t, e.working.Notify[setup.NotifyPrefOutgoingTx])
}

// prefCheck looks up a pref's check widget with nil + missing-key
// guards so nilaway can see the access is safe and tests fail loudly
// if a pref disappears from the editor.
func prefCheck(t *testing.T, e *RulesEditor, key string) *widget.Check {
	t.Helper()
	require.NotNil(t, e)
	chk, ok := e.prefChecks[key]
	require.True(t, ok, "missing pref %q", key)
	require.NotNil(t, chk, "nil check for pref %q", key)
	return chk
}

func TestRulesEditorEverythingToggleHidesTargets(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)

	require.True(t, e.targetsBox.Visible())
	e.everythingCheck.OnChanged(true)
	assert.False(t, e.targetsBox.Visible())
	assert.True(t, e.working.Filter.MonitorEverything)

	e.everythingCheck.OnChanged(false)
	assert.True(t, e.targetsBox.Visible())
	assert.False(t, e.working.Filter.MonitorEverything)
}

// TestRulesEditorHydratesMonitorEverythingWithoutFiringOnChanged guards
// that opening the editor with MonitorEverything=true hides the
// targets box at construction time WITHOUT relying on Fyne firing
// OnChanged from SetChecked — the prefs hydration path documents the
// same invariant.
func TestRulesEditorHydratesMonitorEverythingWithoutFiringOnChanged(t *testing.T) {
	test.NewApp()
	plan := samplePlan()
	plan.Filter.MonitorEverything = true
	e := NewRulesEditor(plan, nil)
	assert.True(t, e.everythingCheck.Checked)
	assert.False(t, e.targetsBox.Visible(),
		"targetsBox must be hidden at hydration when "+
			"MonitorEverything is true")
}

func TestRulesEditorAddTargetSnapshotsFilter(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)

	// Add a pool target via the section's add helper.
	e.pools.add("pool1ws7gpqkw4wpdj33lf3hcjy9zk5pxr8htnnxkxepe49p5gp3srcg")
	assert.Contains(t, e.working.Filter.Pools,
		"pool1ws7gpqkw4wpdj33lf3hcjy9zk5pxr8htnnxkxepe49p5gp3srcg")
}

func TestRulesEditorHydratesAndSnapshotsTargetConnectors(t *testing.T) {
	test.NewApp()
	plan := samplePlan()
	plan.Filter.DRepMatch = setup.AdvancedMatchAll
	plan.Filter.AssetMatch = setup.AdvancedMatchAll

	e := NewRulesEditor(plan, nil)
	assert.Equal(t, connectorAndLabel, e.drepConnector.Selected)
	assert.Equal(t, connectorOrLabel, e.poolConnector.Selected)
	assert.Equal(t, connectorAndLabel, e.assetConnector.Selected)

	e.policyConnector.SetSelected(connectorAndLabel)
	assert.Equal(t, setup.AdvancedMatchAll,
		e.working.Filter.PolicyMatch)
}

func TestRulesEditorOnApplyDisablesButtonAndPassesWorking(t *testing.T) {
	test.NewApp()
	var gotPlan setup.SetupPlan
	e := NewRulesEditor(samplePlan(), func(
		_ context.Context,
		plan setup.SetupPlan,
		_ *RulesEditor,
	) {
		gotPlan = plan
	})
	// Mutate then apply; gotPlan must reflect the mutation.
	prefCheck(t, e, setup.NotifyPrefVotesCast).OnChanged(true)
	e.onApply()

	assert.True(t, e.applyBtn.Disabled())
	assert.True(t, gotPlan.Notify[setup.NotifyPrefVotesCast])

	e.EnableButtons()
	assert.False(t, e.applyBtn.Disabled())
}

// TestRulesEditorOnApplyDeepCopiesNotify asserts the callback receives
// a plan whose Notify map is independent of the editor's working map,
// so post-Apply UI mutations cannot race the goroutine's reads. The
// test sets a key TRUE before Apply then flips it FALSE after — a
// shared-map regression would let the post-Apply flip propagate into
// gotPlan, failing the assertion (a weaker test that wrote a key the
// editor never set would pass under a shared-map regression).
func TestRulesEditorOnApplyDeepCopiesNotify(t *testing.T) {
	test.NewApp()
	var gotPlan setup.SetupPlan
	e := NewRulesEditor(samplePlan(), func(
		_ context.Context,
		plan setup.SetupPlan,
		_ *RulesEditor,
	) {
		gotPlan = plan
	})
	// Pre-toggle VotesCast TRUE so onApply ships a plan carrying it.
	prefCheck(t, e, setup.NotifyPrefVotesCast).OnChanged(true)
	require.True(t, e.working.Notify[setup.NotifyPrefVotesCast])
	e.onApply()
	require.True(t, gotPlan.Notify[setup.NotifyPrefVotesCast],
		"callback should have received the pre-Apply state")
	// Flip e.working.Notify[VotesCast] to false AFTER Apply. A shared
	// map alias would propagate that flip to gotPlan.
	e.working.Notify[setup.NotifyPrefVotesCast] = false
	assert.True(t, gotPlan.Notify[setup.NotifyPrefVotesCast],
		"callback plan must be a deep copy of working — "+
			"post-Apply flip must not propagate")
	// Filter slices must also be independent.
	e.working.Filter.Wallets = append(e.working.Filter.Wallets, "addr1z")
	assert.NotContains(t, gotPlan.Filter.Wallets, "addr1z")
}

// TestRulesEditorOnApplyFreezesAllInputs asserts that Apply disables
// every input that mutates working, so the user cannot mutate state
// the background goroutine is reading.
func TestRulesEditorOnApplyFreezesAllInputs(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)
	e.onApply()
	assert.True(t, e.applyBtn.Disabled())
	assert.True(t, e.everythingCheck.Disabled())
	for pref, c := range e.prefChecks {
		assert.True(t, c.Disabled(), "pref %q not frozen", pref)
	}
	for _, sec := range []*targetSection{
		e.wallets, e.dreps, e.pools, e.assets, e.policies,
	} {
		assert.True(t, sec.addBtn.Disabled(),
			"%s add button not frozen", sec.label)
		assert.True(t, sec.entry.Disabled(),
			"%s entry not frozen", sec.label)
		for i, b := range sec.rowBtns {
			assert.True(t, b.Disabled(),
				"%s row %d trash button not frozen", sec.label, i)
		}
	}

	e.EnableButtons()
	assert.False(t, e.applyBtn.Disabled())
	assert.False(t, e.everythingCheck.Disabled())
	for _, c := range e.prefChecks {
		assert.False(t, c.Disabled())
	}
	for _, sec := range []*targetSection{
		e.wallets, e.dreps, e.pools, e.assets, e.policies,
	} {
		assert.False(t, sec.addBtn.Disabled())
		assert.False(t, sec.entry.Disabled())
		for _, b := range sec.rowBtns {
			assert.False(t, b.Disabled())
		}
	}
}

// TestSnapshotFilterNilsSlicesWhenMonitorEverything asserts that
// flipping the Monitor Everything toggle and snapshotting drops the
// per-target lists (parity with wizard step3 Apply) so they do not
// resurrect on the next editor open.
func TestSnapshotFilterNilsSlicesWhenMonitorEverything(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)
	require.NotEmpty(t, e.wallets.values)

	e.everythingCheck.OnChanged(true)
	e.snapshotFilter()
	assert.True(t, e.working.Filter.MonitorEverything)
	assert.Nil(t, e.working.Filter.Wallets)
	assert.Nil(t, e.working.Filter.DReps)
	assert.Nil(t, e.working.Filter.Pools)
	assert.Nil(t, e.working.Filter.Assets)
	assert.Nil(t, e.working.Filter.Policies)
}

func TestRulesEditorOnApplyNilCallbackNoPanic(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)
	// Should not panic even with nil callback.
	e.onApply()
	assert.True(t, e.applyBtn.Disabled())
}

func TestShowRulesEditorCreatesWindow(t *testing.T) {
	test.NewApp()
	ShowRulesEditor(samplePlan(), nil)
	assert.NotNil(t, test.Canvas())
}

// findButton walks a fyne object tree (containers + widget renderers)
// and returns the first *widget.Button whose visible text matches
// label. Used to drive dialog confirm/dismiss buttons.
func findButton(obj fyne.CanvasObject, label string) *widget.Button {
	if obj == nil {
		return nil
	}
	if b, ok := obj.(*widget.Button); ok && b.Text == label {
		return b
	}
	switch v := obj.(type) {
	case *fyne.Container:
		for _, c := range v.Objects {
			if got := findButton(c, label); got != nil {
				return got
			}
		}
	case fyne.Widget:
		r := test.WidgetRenderer(v)
		if r == nil {
			return nil
		}
		for _, c := range r.Objects() {
			if got := findButton(c, label); got != nil {
				return got
			}
		}
	}
	return nil
}

func tapOverlayButton(t *testing.T, w fyne.Window, label string) bool {
	t.Helper()
	top := w.Canvas().Overlays().Top()
	if top == nil {
		return false
	}
	btn := findButton(top, label)
	if btn == nil {
		return false
	}
	btn.OnTapped()
	return true
}

// TestRulesEditorDeleteTargetConfirms drives a section's trash button
// in the editor context (onDelete wraps deletion in a confirm dialog).
// The confirm dialog must appear; tapping Yes drops the value, tapping
// No keeps it.
func TestRulesEditorDeleteTargetConfirms(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)

	// The wallet section has exactly one row from samplePlan. Its
	// row container holds a trash button (icon-only).
	require.Len(t, e.wallets.values, 1)
	row, ok := e.wallets.list.Objects[0].(*fyne.Container)
	require.True(t, ok)

	// Find the trash button (the only widget.Button in the row).
	var trash *widget.Button
	for _, o := range row.Objects {
		if b, ok := o.(*widget.Button); ok {
			trash = b
			break
		}
	}
	require.NotNil(t, trash, "trash button not found in row")

	// Tap trash: confirm dialog must appear.
	trash.OnTapped()
	require.NotNil(t, e.window.Canvas().Overlays().Top(),
		"confirm dialog did not open")

	// Tap No: value preserved.
	require.True(t, tapOverlayButton(t, e.window, "No"))
	assert.Len(t, e.wallets.values, 1)

	// Tap trash again, this time confirm.
	trash.OnTapped()
	require.True(t, tapOverlayButton(t, e.window, "Yes"))
	assert.Empty(t, e.wallets.values)
	// values and the visible list must stay in sync after a delete.
	assert.Empty(t, e.wallets.list.Objects,
		"visible list must shrink with values")
	e.snapshotFilter()
	assert.Empty(t, e.working.Filter.Wallets)
}

// TestTargetSectionAddRejectsDuplicate asserts add() rejects a value
// already present (and within the same batch), so values stays unique
// and removeValue's "find first match" loop cannot desync the slice
// from the visible list.
func TestTargetSectionAddRejectsDuplicate(t *testing.T) {
	test.NewApp()
	sec := newTargetSection("Wallets", "addr",
		setup.ValidateWalletAddr, nil)
	sec.add(
		"addr1q9hlrf6lmtgu7mqupwlysw7qcvexmjkmwfynqlfh33dz87xy3y67g60shkajwfsewt2tjs85a3xkpkmcafpwwzpevlcsmwzj82",
	)
	require.Len(t, sec.values, 1)

	// Same value again -> rejected via errLabel, values unchanged.
	sec.add(
		"addr1q9hlrf6lmtgu7mqupwlysw7qcvexmjkmwfynqlfh33dz87xy3y67g60shkajwfsewt2tjs85a3xkpkmcafpwwzpevlcsmwzj82",
	)
	assert.Len(t, sec.values, 1)
	assert.True(t, sec.errLabel.Visible())
	assert.Contains(t, sec.errLabel.Text, "already in")

	// Comma-separated batch with internal duplicate -> all rejected.
	sec.add(
		"addr1q8j55h0kfan5dxkj57nu9zkv0w2c8py6gvgct69wxptevh89kxh4rln29df4q2pnfcc4y58pjjrev0qfvxv5j93s5e7sx2g78c," +
			"addr1q8j55h0kfan5dxkj57nu9zkv0w2c8py6gvgct69wxptevh89kxh4rln29df4q2pnfcc4y58pjjrev0qfvxv5j93s5e7sx2g78c",
	)
	assert.Len(t, sec.values, 1)
}

func TestTargetSectionExplainsAnyMatchAndUpdatesCount(t *testing.T) {
	test.NewApp()
	sec := newTargetSection("Wallets", "addr",
		setup.ValidateWalletAddr, nil)

	assert.Equal(t, "Any wallet saved here can match.", sec.matchHint.Text)
	assert.False(t, sec.countLabel.Visible())

	sec.setValues([]string{"addr1first", "addr1second"})
	assert.Equal(t, "2 saved", sec.countLabel.Text)

	sec.removeValue("addr1first", sec.list.Objects[0])
	assert.Equal(t, "1 saved", sec.countLabel.Text)
}

// TestTargetSectionAddDedupIsCaseInsensitive guards that two hex IDs
// differing only in case are recognised as the same on-chain identity.
// PolicyID/PoolID/DRepID/AssetFingerprint round-trip through
// hex.DecodeString which accepts mixed case, so without case-fold
// dedup the rules engine would emit duplicate per-target rules for
// the same hash.
func TestTargetSectionAddDedupIsCaseInsensitive(t *testing.T) {
	test.NewApp()
	sec := newTargetSection("Policies", "56-char hex",
		setup.ValidatePolicyID, nil)
	const lower = "0123456789abcdef0123456789abcdef0123456789abcdef01234567"
	sec.add(lower)
	require.Len(t, sec.values, 1)
	upper := "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF01234567"
	sec.add(upper)
	assert.Len(t, sec.values, 1,
		"upper/lower hex must dedup — same on-chain policy")
	assert.True(t, sec.errLabel.Visible())
}

// TestTargetSectionSetValuesDedupIsCaseInsensitive guards the same
// invariant on the hydration path (legacy YAML may have stored
// case-variant duplicates from a pre-dedup release).
func TestTargetSectionSetValuesDedupIsCaseInsensitive(t *testing.T) {
	test.NewApp()
	sec := newTargetSection("Policies", "56-char hex",
		setup.ValidatePolicyID, nil)
	sec.setValues([]string{
		"0123456789abcdef0123456789abcdef0123456789abcdef01234567",
		"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF01234567",
	})
	assert.Len(t, sec.values, 1,
		"setValues silently drops case-variant duplicates")
	assert.Len(t, sec.rowBtns, 1,
		"rowBtns must stay in lock-step with values")
}

// TestTargetSectionNoConfirmWhenOnDeleteNil verifies that the shared
// section keeps its immediate-delete behaviour when used outside the
// editor (i.e. onDelete is nil — wizard step 3 path).
func TestTargetSectionNoConfirmWhenOnDeleteNil(t *testing.T) {
	test.NewApp()
	called := 0
	sec := newTargetSection("Wallets", "addr",
		setup.ValidateWalletAddr,
		func() { called++ })
	sec.setValues([]string{
		"addr1q9hlrf6lmtgu7mqupwlysw7qcvexmjkmwfynqlfh33dz87xy3y67g60shkajwfsewt2tjs85a3xkpkmcafpwwzpevlcsmwzj82",
	})

	row, ok := sec.list.Objects[0].(*fyne.Container)
	require.True(t, ok)
	var trash *widget.Button
	for _, o := range row.Objects {
		if b, ok := o.(*widget.Button); ok {
			trash = b
			break
		}
	}
	require.NotNil(t, trash)

	trash.OnTapped()
	assert.Empty(t, sec.values, "delete must be immediate when no win")
	assert.Equal(t, 1, called, "onChange must fire after delete")
}

// TestRulesEditorOnApplyBlocksCloseAndCancel guards that Apply
// freezes both the Cancel button AND the native window-close
// affordance so the warning dialog's parent cannot be torn down
// mid-apply. EnableButtons restores both.
func TestRulesEditorOnApplyBlocksCloseAndCancel(t *testing.T) {
	test.NewApp()
	e := NewRulesEditor(samplePlan(), nil)
	require.False(t, e.applying)
	require.False(t, e.closeBtn.Disabled())

	e.onApply()
	assert.True(t, e.applying)
	assert.True(t, e.closeBtn.Disabled(),
		"Cancel must be frozen during apply")

	e.EnableButtons()
	assert.False(t, e.applying)
	assert.False(t, e.closeBtn.Disabled())
}

// TestRulesEditorHydratesMissingPrefsToTrue guards parity with the
// setup wizard's default-on behavior: a pref absent from the saved
// plan must be checked in the editor and ship through Apply as true,
// so sparse / upgraded configs do not silently opt out.
func TestRulesEditorHydratesMissingPrefsToTrue(t *testing.T) {
	test.NewApp()
	plan := samplePlan()
	// Strip every pref from the plan so all are "missing".
	plan.Notify = setup.NotificationPrefs{}
	var gotPlan setup.SetupPlan
	e := NewRulesEditor(plan, func(
		_ context.Context, p setup.SetupPlan, _ *RulesEditor,
	) {
		gotPlan = p
	})
	for _, pref := range setup.AllNotifyPrefs() {
		chk := prefCheck(t, e, pref)
		assert.Truef(t, chk.Checked,
			"missing pref %q must hydrate as on", pref)
	}
	e.onApply()
	for _, pref := range setup.AllNotifyPrefs() {
		assert.Truef(t, gotPlan.Notify[pref],
			"missing pref %q must ship through Apply as true", pref)
	}
}
