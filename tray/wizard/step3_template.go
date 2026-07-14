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
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
)

// templateStep is the rebuilt step 3 of the wizard: an exclusive
// "Monitor Everything" toggle at the top, then three editable target
// sections (Wallets / DReps / Pools) when the toggle is off, plus the
// existing optional output destination selector and a persistent
// "Current configuration" summary line.
type templateStep struct {
	plan *setup.SetupPlan

	everythingCheck *widget.Check
	drepConnector   *widget.RadioGroup
	poolConnector   *widget.RadioGroup
	assetConnector  *widget.RadioGroup
	policyConnector *widget.RadioGroup
	targetsBox      *fyne.Container
	wallets         *targetSection
	dreps           *targetSection
	pools           *targetSection
	assets          *targetSection
	policies        *targetSection
	summaryLabel    *widget.Label

	// Output destination (unchanged from the previous wizard).
	outputSelect    *widget.Select
	outputContainer *fyne.Container
	webhookURL      *widget.Entry
	webhookFormat   *widget.Select
	telegramToken   *widget.Entry
	telegramChat    *widget.Entry
	logFilePath     *widget.Entry
}

func (s *templateStep) Title() string { return "Events & Outputs" }
func (s *templateStep) Description() string {
	return "Choose what to monitor and where to send notifications."
}

func (s *templateStep) Content() fyne.CanvasObject {
	// Sections are constructed on first Content() call; subsequent
	// calls (e.g. when navigating back) reuse the same widgets so the
	// user's in-progress entries survive.
	if s.wallets == nil {
		s.wallets = s.newSection(
			"Wallets",
			"Cardano address (addr1... or stake1...)",
			setup.ValidateWalletAddr,
		)
		s.dreps = s.newSection(
			"DReps",
			"DRep ID (drep1... or hex)",
			setup.ValidateDRepID,
		)
		s.pools = s.newSection(
			"Pools",
			"Pool ID (pool1... or hex)",
			setup.ValidatePoolID,
		)
		s.assets = s.newSection(
			"Assets",
			"Asset fingerprint (asset1... — CIP-14)",
			setup.ValidateAssetFingerprint,
		)
		s.policies = s.newSection(
			"Policies",
			"Policy ID (56-character hex)",
			setup.ValidatePolicyID,
		)
		s.summaryLabel = widget.NewLabel("")
		s.summaryLabel.TextStyle = fyne.TextStyle{Italic: true}

		filter := setup.FilterConfig{}
		if s.plan != nil {
			filter = s.plan.Filter
		}
		s.wallets.setValues(filter.Wallets)
		s.dreps.setValues(filter.DReps)
		s.pools.setValues(filter.Pools)
		s.assets.setValues(filter.Assets)
		s.policies.setValues(filter.Policies)

		s.targetsBox = container.NewVBox()
		s.drepConnector = newTargetConnector(
			filter.ResolvedDRepMatch(), func(string) { s.refreshSummary() },
		)
		s.poolConnector = newTargetConnector(
			filter.ResolvedPoolMatch(), func(string) { s.refreshSummary() },
		)
		s.assetConnector = newTargetConnector(
			filter.ResolvedAssetMatch(), func(string) { s.refreshSummary() },
		)
		s.policyConnector = newTargetConnector(
			filter.ResolvedPolicyMatch(), func(string) { s.refreshSummary() },
		)
		s.everythingCheck = widget.NewCheck(
			"Monitor Everything (ignore per-target lists)",
			func(checked bool) {
				if checked {
					s.targetsBox.Hide()
				} else {
					s.targetsBox.Show()
				}
				s.refreshSummary()
			},
		)

		if s.plan != nil {
			s.everythingCheck.SetChecked(
				s.plan.Filter.MonitorEverything,
			)
		}
		s.refreshTargetMode()
		s.refreshSummary()
	}

	if s.outputSelect == nil {
		s.buildOutputSelector()
	}

	monitorBox := container.NewVBox(
		widget.NewLabelWithStyle(
			"Monitoring Targets",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		newFilterLogicLabel(filterLogicDescription),
		s.everythingCheck,
		s.targetsBox,
		s.summaryLabel,
	)

	outputBox := container.NewVBox(
		widget.NewSeparator(),
		widget.NewLabelWithStyle(
			"External Output Destination (Optional)",
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		),
		widget.NewLabel(
			"How should events be delivered externally? "+
				"(optional; desktop notifications always work "+
				"via the tray)",
		),
		s.outputSelect,
		s.outputContainer,
	)

	// No Spacer between the boxes: the outer content scrolls (see
	// wizard.updateStep), and in a squeezed box layout a Spacer takes
	// negative height, overlapping outputBox onto monitorBox. outputBox
	// already opens with a separator for visual division.
	return container.NewVBox(
		monitorBox,
		outputBox,
	)
}

// newSection wires up one editable target list for the templateStep,
// delegating to the shared newTargetSection (see target_section.go).
func (s *templateStep) newSection(
	label, placeholder string,
	validate func(string) error,
) *targetSection {
	return newTargetSection(
		label, placeholder, validate, s.onTargetsChanged,
	)
}

// onTargetsChanged fires after any section add/remove: rebuild the target
// layout (so connector visibility tracks which groups are populated) and
// refresh the summary line.
func (s *templateStep) onTargetsChanged() {
	s.refreshTargetMode()
	s.refreshSummary()
}

func (s *templateStep) refreshTargetMode() {
	s.targetsBox.RemoveAll()
	// Sections always render (they must accept input even when empty). A
	// group's connector renders only when that group AND some earlier group
	// are populated, since the connector joins the group to the previous
	// populated one (see standardFilterMatcher) and is meaningless
	// otherwise — this hides orphan connectors framing empty sections.
	rows := []struct {
		sec       *targetSection
		connector *widget.RadioGroup
	}{
		{s.wallets, nil},
		{s.dreps, s.drepConnector},
		{s.pools, s.poolConnector},
		{s.assets, s.assetConnector},
		{s.policies, s.policyConnector},
	}
	earlier := false
	for i, r := range rows {
		populated := len(r.sec.values) > 0
		if r.connector != nil && populated && earlier {
			s.targetsBox.Add(connectorRow(r.connector))
		}
		if i > 0 {
			s.targetsBox.Add(widget.NewSeparator())
		}
		s.targetsBox.Add(r.sec.canvasObject())
		if populated {
			earlier = true
		}
	}
}

func (s *templateStep) currentFilter() setup.FilterConfig {
	filter := setup.FilterConfig{
		MonitorEverything: s.everythingCheck.Checked,
		Wallets:           append([]string(nil), s.wallets.values...),
		DReps:             append([]string(nil), s.dreps.values...),
		Pools:             append([]string(nil), s.pools.values...),
		Assets:            append([]string(nil), s.assets.values...),
		Policies:          append([]string(nil), s.policies.values...),
		DRepMatch:         connectorMode(s.drepConnector),
		PoolMatch:         connectorMode(s.poolConnector),
		AssetMatch:        connectorMode(s.assetConnector),
		PolicyMatch:       connectorMode(s.policyConnector),
	}
	if filter.MonitorEverything {
		return setup.FilterConfig{MonitorEverything: true}
	}
	return filter
}

func (s *templateStep) refreshSummary() {
	if s.summaryLabel == nil || s.everythingCheck == nil {
		return
	}
	s.summaryLabel.SetText(
		"Current configuration: " + setup.SummarizeFilter(s.currentFilter()),
	)
}

func (s *templateStep) buildOutputSelector() {
	outputs := []string{
		"None (desktop notifications only)",
		"Webhook",
		"Telegram",
		"Log to File",
	}
	s.outputSelect = widget.NewSelect(outputs, s.onOutputChange)
	s.outputContainer = container.NewVBox()

	initialOutput := outputs[0]
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

// Validate gates the wizard's Next button. When MonitorEverything is
// off the user must have configured at least one target (otherwise the
// wizard would produce a plan that matches nothing). The output
// sub-validations are unchanged from the previous step3.
func (s *templateStep) Validate() error {
	if !s.everythingCheck.Checked {
		filter := s.currentFilter()
		hasTarget := len(filter.Wallets) > 0 || len(filter.DReps) > 0 ||
			len(filter.Pools) > 0 || len(filter.Assets) > 0 ||
			len(filter.Policies) > 0
		if !hasTarget {
			return errors.New(
				"add at least one wallet, DRep, pool, asset, or policy " +
					"or enable Monitor Everything",
			)
		}
		if filter.MatchesNothing() {
			return errors.New(
				"this AND combination can never match: it joins targets " +
					"from different event types (pools match blocks, " +
					"wallets/assets/policies match transactions, DReps " +
					"match governance) — use OR between them, or remove one",
			)
		}
	}

	switch s.outputSelect.Selected {
	case "Webhook":
		if s.webhookURL.Text == "" {
			return errors.New("webhook URL is required")
		}
	case "Telegram":
		if s.telegramToken.Text == "" || s.telegramChat.Text == "" {
			return errors.New(
				"telegram token and chat ID are required",
			)
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
			return errors.New(
				"log path must be a file, not a directory",
			)
		}
		if st, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf(
					"directory does not exist: %s", dir,
				)
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
	plan.Filter = s.currentFilter()

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
