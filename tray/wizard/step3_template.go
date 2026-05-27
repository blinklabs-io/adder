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
	"log/slog"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
)

type templateStep struct {
	plan *setup.SetupPlan

	selectedTemplate string
	templateParam    *widget.Entry
	validationLabel  *widget.Label

	watchWalletCard *widget.Button
	trackDRepCard   *widget.Button
	monitorPoolCard *widget.Button
	everythingCard  *widget.Button

	outputSelect    *widget.Select
	outputContainer *fyne.Container

	// Webhook fields
	webhookURL    *widget.Entry
	webhookFormat *widget.Select

	// Telegram fields
	telegramToken *widget.Entry
	telegramChat  *widget.Entry

	// Log file fields
	logFilePath *widget.Entry
}

func (s *templateStep) Title() string { return "Events & Outputs" }
func (s *templateStep) Description() string {
	return "Choose what to monitor and where to send notifications."
}

func (s *templateStep) Content() fyne.CanvasObject {
	if s.watchWalletCard != nil {
		templateCards := container.NewGridWithColumns(2,
			s.watchWalletCard,
			s.trackDRepCard,
			s.monitorPoolCard,
			s.everythingCard,
		)

		templateBox := container.NewVBox(
			widget.NewLabelWithStyle(
				"Monitoring Template",
				fyne.TextAlignLeading,
				fyne.TextStyle{Bold: true},
			),
			templateCards,
			s.templateParam,
			s.validationLabel,
		)

		outputBox := container.NewVBox(
			widget.NewSeparator(),
			widget.NewLabelWithStyle(
				"External Output Destination (Optional)",
				fyne.TextAlignLeading,
				fyne.TextStyle{Bold: true},
			),
			widget.NewLabel("How should events be delivered externally? (optional; desktop notifications always work via the tray)"),
			s.outputSelect,
			s.outputContainer,
		)

		return container.NewVBox(
			templateBox,
			layout.NewSpacer(),
			outputBox,
		)
	}

	// Template Selection
	s.watchWalletCard = widget.NewButton(
		"Watch Wallet",
		func() { s.selectTemplate("Watch Wallet") },
	)
	s.trackDRepCard = widget.NewButton(
		"Track DRep",
		func() { s.selectTemplate("Track DRep") },
	)
	s.monitorPoolCard = widget.NewButton(
		"Monitor Pool",
		func() { s.selectTemplate("Monitor Pool") },
	)
	s.everythingCard = widget.NewButton(
		"Monitor Everything",
		func() { s.selectTemplate("Monitor Everything") },
	)

	s.templateParam = widget.NewMultiLineEntry()
	s.templateParam.SetMinRowsVisible(3)
	s.templateParam.OnChanged = func(t string) { s.validateParam(t) }
	s.templateParam.Hide()

	s.validationLabel = widget.NewLabel("")
	s.validationLabel.Hide()

	templateCards := container.NewGridWithColumns(2,
		s.watchWalletCard,
		s.trackDRepCard,
		s.monitorPoolCard,
		s.everythingCard,
	)

	templateBox := container.NewVBox(
		widget.NewLabelWithStyle(
			"Monitoring Template",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		templateCards,
		s.templateParam,
		s.validationLabel,
	)

	// Output Selection
	outputs := []string{
		"None (desktop notifications only)",
		"Webhook",
		"Telegram",
		"Log to File",
	}
	s.outputSelect = widget.NewSelect(outputs, s.onOutputChange)

	s.outputContainer = container.NewVBox()

	outputBox := container.NewVBox(
		widget.NewSeparator(),
		widget.NewLabelWithStyle(
			"External Output Destination (Optional)",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		widget.NewLabel("How should events be delivered externally? (optional; desktop notifications always work via the tray)"),
		s.outputSelect,
		s.outputContainer,
	)

	// Initial values from plan
	initialTemplate := "Watch Wallet"
	if s.plan != nil && s.plan.Filter.Template != "" {
		initialTemplate = s.plan.Filter.Template
	}
	s.selectTemplate(initialTemplate)
	if s.plan != nil {
		s.templateParam.SetText(s.plan.Filter.Param)
	}

	initialOutput := outputs[0] // None
	if s.plan != nil {
		switch s.plan.Output.Type {
		case "webhook":
			initialOutput = "Webhook"
		case "telegram":
			initialOutput = "Telegram"
		case "log":
			initialOutput = "Log to File"
		}
	}
	s.outputSelect.SetSelected(initialOutput)

	return container.NewVBox(
		templateBox,
		layout.NewSpacer(),
		outputBox,
	)
}

func (s *templateStep) selectTemplate(name string) {
	s.selectedTemplate = name
	s.watchWalletCard.Importance = widget.LowImportance
	s.trackDRepCard.Importance = widget.LowImportance
	s.monitorPoolCard.Importance = widget.LowImportance
	s.everythingCard.Importance = widget.LowImportance

	placeholder := ""
	showParam := true

	switch name {
	case "Watch Wallet":
		s.watchWalletCard.Importance = widget.HighImportance
		placeholder = "Cardano address(es) (addr1..., stake...)\n" +
			"Separate multiple with commas"
	case "Track DRep":
		s.trackDRepCard.Importance = widget.HighImportance
		placeholder = "DRep ID(s) (bech32 or hex)\n" +
			"Separate multiple with commas"
	case "Monitor Pool":
		s.monitorPoolCard.Importance = widget.HighImportance
		placeholder = "Pool ID(s) (pool1... or hex)\n" +
			"Separate multiple with commas"
	case "Monitor Everything":
		s.everythingCard.Importance = widget.HighImportance
		showParam = false
	}

	s.templateParam.SetPlaceHolder(placeholder)
	if showParam {
		s.templateParam.Show()
	} else {
		s.templateParam.Hide()
	}
	s.validateParam(s.templateParam.Text)

	s.watchWalletCard.Refresh()
	s.trackDRepCard.Refresh()
	s.monitorPoolCard.Refresh()
	s.everythingCard.Refresh()
}

func (s *templateStep) validateParam(text string) {
	if text == "" {
		s.validationLabel.Hide()
		return
	}

	if err := setup.ValidateTemplateParam(s.selectedTemplate, text); err != nil {
		s.validationLabel.SetText(err.Error())
		s.validationLabel.Show()
	} else {
		s.validationLabel.Hide()
	}
}

func (s *templateStep) onOutputChange(selected string) {
	if s.outputContainer == nil {
		return
	}
	s.outputContainer.Objects = nil
	switch selected {
	case "Webhook":
		s.webhookURL = widget.NewEntry()
		s.webhookURL.SetPlaceHolder("https://hooks.slack.com/...")
		if s.plan != nil {
			s.webhookURL.SetText(s.plan.Output.Config["url"])
		}

		s.webhookFormat = widget.NewSelect(
			[]string{"adder", "discord"},
			func(string) {},
		)
		initialFormat := "adder"
		if s.plan != nil {
			if f, ok := s.plan.Output.Config["format"]; ok {
				initialFormat = f
			}
		}
		s.webhookFormat.SetSelected(initialFormat)

		s.outputContainer.Add(widget.NewForm(
			widget.NewFormItem("Webhook URL", s.webhookURL),
			widget.NewFormItem("Format", s.webhookFormat),
		))
	case "Telegram":
		s.telegramToken = widget.NewEntry()
		s.telegramToken.SetPlaceHolder("123456:ABC-DEF...")
		if s.plan != nil {
			s.telegramToken.SetText(s.plan.Output.Config["token"])
		}

		s.telegramChat = widget.NewEntry()
		s.telegramChat.SetPlaceHolder("Chat ID or @channel")
		if s.plan != nil {
			s.telegramChat.SetText(s.plan.Output.Config["chat_id"])
		}

		s.outputContainer.Add(widget.NewForm(
			widget.NewFormItem("Bot Token", s.telegramToken),
			widget.NewFormItem("Chat ID", s.telegramChat),
		))
	case "Log to File":
		s.logFilePath = widget.NewEntry()
		s.logFilePath.SetPlaceHolder("~/Downloads/adder.log")
		if s.plan != nil {
			s.logFilePath.SetText(s.plan.Output.Config["path"])
		}

		s.outputContainer.Add(widget.NewForm(
			widget.NewFormItem("Log File Path", s.logFilePath),
		))
	}
	s.outputContainer.Refresh()
}

func (s *templateStep) Validate() error {
	if s.selectedTemplate != "Monitor Everything" && s.templateParam.Text == "" {
		return errors.New("template parameter is required")
	}
	// Re-run validation logic
	s.validateParam(s.templateParam.Text)
	if s.validationLabel.Visible() {
		return errors.New("invalid template parameter format")
	}

	switch s.outputSelect.Selected {
	case "Webhook":
		if s.webhookURL.Text == "" {
			return errors.New("webhook URL is required")
		}
	case "Telegram":
		if s.telegramToken.Text == "" || s.telegramChat.Text == "" {
			return errors.New("telegram token and chat ID are required")
		}
	case "Log to File":
		if s.logFilePath.Text == "" {
			return errors.New("log file path is required")
		}
		path, err := setup.ExpandTildePath(s.logFilePath.Text)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return errors.New("log path must be a file, not a directory")
		}
		if st, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("directory does not exist: %s", dir)
			}
			return fmt.Errorf("failed to access directory %s: %w",
				dir, err)
		} else if !st.IsDir() {
			return fmt.Errorf("path is not a directory: %s", dir)
		}
	}

	return nil
}

func (s *templateStep) Apply(plan *setup.SetupPlan) {
	plan.Filter.Template = s.selectedTemplate
	plan.Filter.Param = s.templateParam.Text

	plan.Output.Config = make(map[string]string)
	switch s.outputSelect.Selected {
	case "Webhook":
		plan.Output.Type = "webhook"
		plan.Output.Config["url"] = s.webhookURL.Text
		plan.Output.Config["format"] = s.webhookFormat.Selected
	case "Telegram":
		plan.Output.Type = "telegram"
		plan.Output.Config["token"] = s.telegramToken.Text
		plan.Output.Config["chat_id"] = s.telegramChat.Text
	case "Log to File":
		plan.Output.Type = "log"
		path := s.logFilePath.Text
		// Robust home directory expansion
		if expanded, err := setup.ExpandTildePath(path); err == nil {
			path = expanded
		} else {
			slog.Error("failed to expand tilde path",
				"error", err)
		}
		plan.Output.Config["path"] = path
		plan.Output.Config["format"] = "json"
	default:
		plan.Output.Type = "none"
	}
}
