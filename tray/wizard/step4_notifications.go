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
	"runtime"

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

	permBtn := widget.NewButtonWithIcon("Send Test Notification", theme.ConfirmIcon(), func() {
		// Trigger the native permission dialog
		fyne.CurrentApp().SendNotification(fyne.NewNotification(
			"Adder Verification",
			"Did this notification appear? If so, "+
				"permissions are correctly set.",
		))
		s.verifyBox.Show()
		s.verifyResult.SetText("A test notification was sent. Did you see it?")
		s.verifyResult.Show()
	})
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
	if s.plan != nil && s.plan.Output.Type != "" && s.plan.Output.Type != "none" {
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
		widget.NewLabelWithStyle("System Verification", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("macOS requires explicit permission for notifications."),
		permBtn,
		s.verifyResult,
		s.verifyBox,
		s.summaryLine,
	)
}

func (s *notificationsStep) getCheckLabels() []string {
	switch s.plan.Filter.Template {
	case "Monitor Everything":
		return []string{
			setup.NotifyPrefBlocksMinted,
			setup.NotifyPrefIncomingTx,
			setup.NotifyPrefVotesCast,
		}
	case "Watch Wallet":
		return []string{
			setup.NotifyPrefIncomingTx,
			setup.NotifyPrefOutgoingTx,
			setup.NotifyPrefTokenTransfers,
		}
	case "Track DRep":
		return []string{
			setup.NotifyPrefGovProposals,
			setup.NotifyPrefVotesCast,
			setup.NotifyPrefRegChanges,
		}
	case "Monitor Pool":
		return []string{
			setup.NotifyPrefBlocksMinted,
			setup.NotifyPrefPoolParams,
		}
	}
	return nil
}

func (s *notificationsStep) Validate() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if !s.verified {
		return errors.New("please verify that system notifications are working before finishing")
	}
	return nil
}

func (s *notificationsStep) Apply(plan *setup.SetupPlan) {
	for label, check := range s.checks {
		plan.Notify[label] = check.Checked
	}
	plan.Notify[setup.NotifyPrefConnectionIssues] = s.connection.Checked
}
