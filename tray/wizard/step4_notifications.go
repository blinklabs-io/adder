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
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
)

type notificationsStep struct {
	plan *setup.SetupPlan

	checks      map[string]*widget.Check
	connection  *widget.Check
	summaryLine *widget.Label

	verified     bool
	verifyBox    *fyne.Container
	verifyResult *widget.Label
}

func (s *notificationsStep) Title() string { return "Notifications" }
func (s *notificationsStep) Description() string {
	return "Fine-tune alerts and verify system permissions."
}

func (s *notificationsStep) Content() fyne.CanvasObject {
	// Always reinitialize checks to handle template changes
	s.checks = make(map[string]*widget.Check)
	s.createChecks()

	return s.createLayout()
}

func (s *notificationsStep) createChecks() {
	for _, label := range s.getCheckLabels() {
		initial := true
		if s.plan != nil {
			if val, ok := s.plan.Notify[label]; ok {
				initial = val
			}
		}
		check := widget.NewCheck(label, func(bool) {})
		check.SetChecked(initial)
		s.checks[label] = check
	}

	s.connection = widget.NewCheck("Notify on connection issues", func(bool) {})
	initialConn := true
	if s.plan != nil {
		if val, ok := s.plan.Notify[setup.NotifyPrefConnectionIssues]; ok {
			initialConn = val
		}
	}
	s.connection.SetChecked(initialConn)
}

func (s *notificationsStep) createLayout() fyne.CanvasObject {
	eventBox := container.NewVBox()
	eventBox.Add(
		widget.NewLabelWithStyle(
			"Alert on these events:",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
	)
	for _, label := range s.getCheckLabels() {
		if check, ok := s.checks[label]; ok {
			eventBox.Add(check)
		}
	}

	s.verifyResult = widget.NewLabel("")
	s.verifyResult.Wrapping = fyne.TextWrapWord
	s.verifyResult.Hide()

	permBtn := widget.NewButtonWithIcon(
		"Send Test Notification",
		theme.ConfirmIcon(),
		func() {
			// Trigger the native permission dialog
			fyne.CurrentApp().SendNotification(fyne.NewNotification(
				"Adder Verification",
				"Did this notification appear? If so, "+
					"permissions are correctly set.",
			))
			s.verifyBox.Show()
			s.verifyResult.SetText(
				"A test notification was sent. Did you see it?",
			)
			s.verifyResult.Show()
		},
	)
	permBtn.Importance = widget.HighImportance

	s.verifyBox = container.NewHBox(
		widget.NewButton("Yes, I saw it", func() {
			s.verified = true
			s.verifyResult.SetText("✅ System notifications verified!")
			s.verifyBox.Hide()
		}),
		widget.NewButton("No, it didn't show", func() {
			s.verified = false
			s.verifyResult.SetText("⚠️ Please check 'System Settings > " +
				"Notifications > AdderTray' and ensure 'Allow " +
				"Notifications' is enabled.")
		}),
	)
	s.verifyBox.Hide()

	s.summaryLine = widget.NewLabel("")
	if s.plan != nil && s.plan.Output.Type != "" &&
		s.plan.Output.Type != "none" {
		s.summaryLine.SetText(
			fmt.Sprintf("Events will also be sent to %s.", s.plan.Output.Type),
		)
	}

	return container.NewVBox(
		container.NewHBox(
			widget.NewIcon(theme.SettingsIcon()),
			widget.NewLabel("Notification Preferences"),
		),
		widget.NewSeparator(),
		eventBox,
		widget.NewSeparator(),
		s.connection,
		widget.NewSeparator(),
		widget.NewLabelWithStyle(
			"System Verification",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		widget.NewLabel(
			"macOS requires explicit permission for notifications.",
		),
		permBtn,
		s.verifyResult,
		s.verifyBox,
		s.summaryLine,
	)
}

// getCheckLabels returns the notification-preference checkboxes to show
// for the current plan. When MonitorEverything is on we surface the
// existing coarse set; otherwise we show the deduped union of the
// per-kind prefs whose list is non-empty, so a user watching a wallet
// AND a DRep sees both transaction and governance toggles.
func (s *notificationsStep) getCheckLabels() []string {
	if s.plan.Filter.MonitorEverything {
		return []string{
			setup.NotifyPrefBlocksMinted,
			setup.NotifyPrefIncomingTx,
			setup.NotifyPrefVotesCast,
		}
	}

	// Order matters for UI stability — preserve the per-kind grouping
	// (wallet → DRep → pool) the user is used to. seen guards against
	// duplicates when two kinds share a pref (e.g. NotifyPrefBlocksMinted
	// is relevant for both pool and "everything", but at most one of
	// those branches runs here).
	var out []string
	seen := map[string]bool{}
	add := func(keys ...string) {
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	if len(s.plan.Filter.Wallets) > 0 {
		add(setup.NotifyPrefIncomingTx,
			setup.NotifyPrefOutgoingTx,
			setup.NotifyPrefTokenTransfers)
	}
	if len(s.plan.Filter.DReps) > 0 {
		add(setup.NotifyPrefGovProposals,
			setup.NotifyPrefVotesCast,
			setup.NotifyPrefRegChanges)
	}
	if len(s.plan.Filter.Pools) > 0 {
		add(setup.NotifyPrefBlocksMinted,
			setup.NotifyPrefPoolParams)
	}
	return out
}

// Validate gates the wizard's Next/Finish button. The "Send Test
// Notification" button remains available so the user can confirm macOS
// has granted permission, but it is no longer required — blocking the
// wizard on it was annoying during active development and unnecessary
// in steady state: a user who never sees a notification can re-grant
// permission via System Settings without redoing setup.
func (s *notificationsStep) Validate() error {
	return nil
}

// Apply writes the wizard's notification preferences onto plan,
// rebuilding plan.Notify so stale TRUE keys from earlier wizard runs
// (e.g. user switched from "Watch Wallet" to DRep-only) do not survive
// and re-trigger notifications the user no longer wants. Explicit user
// "no" answers (false values) for prefs that step 4 is not currently
// rendering ARE preserved — otherwise switching templates would silently
// re-default disabled toggles back to true. The connection toggle is
// independent of the target set and always written.
func (s *notificationsStep) Apply(plan *setup.SetupPlan) {
	// Capture explicit user "no" answers for off-display prefs before
	// we replace the map.
	preservedFalse := map[string]bool{}
	for k, v := range plan.Notify {
		if v {
			continue // only preserve explicit negatives
		}
		if _, shown := s.checks[k]; shown {
			continue // the current checkbox value will win
		}
		if k == setup.NotifyPrefConnectionIssues {
			continue // handled below
		}
		preservedFalse[k] = false
	}

	plan.Notify = make(setup.NotificationPrefs,
		len(s.checks)+len(preservedFalse)+1)
	for label, check := range s.checks {
		plan.Notify[label] = check.Checked
	}
	for k := range preservedFalse {
		plan.Notify[k] = false
	}
	plan.Notify[setup.NotifyPrefConnectionIssues] = s.connection.Checked
}
