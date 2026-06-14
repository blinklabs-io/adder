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
	"errors"
	"fmt"
	"strconv"
	"time"

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

	// Rate-limit knobs live behind an "Advanced" accordion. Empty
	// entries are interpreted as "use default" so a user who never
	// opens Advanced still gets sensible behaviour.
	rateLimitEntry  *widget.Entry
	rateWindowEntry *widget.Entry
	rateAdvanced    *widget.Accordion
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

	s.buildAdvancedRateLimit()

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
		widget.NewSeparator(),
		s.rateAdvanced,
	)
}

// buildAdvancedRateLimit constructs the Advanced accordion that lets
// users tune how aggressively the engine coalesces a burst of
// notifications into a "Multiple events occurred" batch.
func (s *notificationsStep) buildAdvancedRateLimit() {
	s.rateLimitEntry = widget.NewEntry()
	s.rateLimitEntry.SetPlaceHolder(
		fmt.Sprintf("%d (default)", setup.DefaultNotifyRateLimit),
	)
	s.rateWindowEntry = widget.NewEntry()
	s.rateWindowEntry.SetPlaceHolder(
		fmt.Sprintf("%s (default)",
			setup.DefaultNotifyRateWindow))

	if s.plan != nil {
		if s.plan.App.NotifyRateLimit != 0 {
			s.rateLimitEntry.SetText(
				strconv.Itoa(s.plan.App.NotifyRateLimit),
			)
		}
		if s.plan.App.NotifyRateWindow > 0 {
			s.rateWindowEntry.SetText(
				s.plan.App.NotifyRateWindow.String(),
			)
		}
	}

	form := widget.NewForm(
		widget.NewFormItem(
			"Max notifications per window", s.rateLimitEntry),
		widget.NewFormItem(
			"Window duration (e.g. 5s, 30s, 1m)", s.rateWindowEntry),
	)
	help := widget.NewLabel(
		"Limits how many alerts fire before they collapse into a " +
			"single \"Multiple events occurred\" notification. " +
			"Set the limit to a negative number to disable " +
			"coalescing entirely. Leave blank to use the defaults.",
	)
	help.Wrapping = fyne.TextWrapWord

	body := container.NewVBox(form, help)
	s.rateAdvanced = widget.NewAccordion(
		widget.NewAccordionItem("Advanced — Rate Limiting", body),
	)
	if s.plan != nil &&
		(s.plan.App.NotifyRateLimit != 0 ||
			s.plan.App.NotifyRateWindow > 0) {
		s.rateAdvanced.Open(0)
	}
}

// getCheckLabels returns the prefs to surface for the current plan:
// the coarse set for MonitorEverything, otherwise the deduped union of
// per-kind prefs whose list is non-empty.
func (s *notificationsStep) getCheckLabels() []string {
	if s.plan.Filter.MonitorEverything {
		return []string{
			setup.NotifyPrefBlocksMinted,
			setup.NotifyPrefIncomingTx,
			setup.NotifyPrefVotesCast,
		}
	}

	// Per-kind ordering for UI stability. seen guards dedup when two
	// kinds share a pref key.
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
	if len(s.plan.Filter.Assets) > 0 {
		add(setup.NotifyPrefAssetActivity)
	}
	if len(s.plan.Filter.Policies) > 0 {
		add(setup.NotifyPrefPolicyActivity)
	}
	return out
}

// Validate rejects malformed Advanced rate-limit entries. "Send Test
// Notification" is available but not required.
func (s *notificationsStep) Validate() error {
	if s.rateLimitEntry != nil && s.rateLimitEntry.Text != "" {
		if _, err := strconv.Atoi(s.rateLimitEntry.Text); err != nil {
			return fmt.Errorf(
				"max notifications per window must be an integer: %w",
				err)
		}
	}
	if s.rateWindowEntry != nil && s.rateWindowEntry.Text != "" {
		d, err := time.ParseDuration(s.rateWindowEntry.Text)
		if err != nil {
			return fmt.Errorf(
				"window duration must be a Go duration (e.g. 5s, "+
					"30s, 1m): %w", err)
		}
		if d <= 0 {
			return errors.New(
				"window duration must be greater than zero")
		}
	}
	return nil
}

// Apply writes preferences onto plan.Notify, rebuilt from s.checks so
// stale keys don't linger. Off-display explicit FALSE values are
// preserved (sticky opt-outs across template switches); off-display
// TRUE values are not — a re-shown checkbox defaults to TRUE, which is
// the desired re-introduction behavior. Connection toggle is always
// written.
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

	// Advanced rate-limit knobs. Empty entries clear the override so
	// the engine falls back to the defaults. Validate has already
	// rejected unparseable values.
	if s.rateLimitEntry != nil {
		if s.rateLimitEntry.Text == "" {
			plan.App.NotifyRateLimit = 0
		} else if n, err := strconv.Atoi(s.rateLimitEntry.Text); err == nil {
			plan.App.NotifyRateLimit = n
		}
	}
	if s.rateWindowEntry != nil {
		if s.rateWindowEntry.Text == "" {
			plan.App.NotifyRateWindow = 0
		} else if d, err := time.ParseDuration(
			s.rateWindowEntry.Text,
		); err == nil {
			plan.App.NotifyRateWindow = d
		}
	}
}
