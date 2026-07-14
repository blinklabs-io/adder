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
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
)

const (
	filterLogicDescription = "Within each group, any saved item can match. Use " +
		"the controls between groups to require AND or allow OR."
	connectorAndLabel = "AND"
	connectorOrLabel  = "OR"
)

func newFilterLogicLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}

func newTargetConnector(
	mode setup.AdvancedMatchMode,
	onChanged func(string),
) *widget.RadioGroup {
	connector := widget.NewRadioGroup(
		[]string{connectorAndLabel, connectorOrLabel}, nil,
	)
	connector.Horizontal = true
	selected := connectorOrLabel
	if mode == setup.AdvancedMatchAll {
		selected = connectorAndLabel
	}
	connector.SetSelected(selected)
	connector.OnChanged = onChanged
	return connector
}

func connectorMode(connector *widget.RadioGroup) setup.AdvancedMatchMode {
	if connector != nil && connector.Selected == connectorAndLabel {
		return setup.AdvancedMatchAll
	}
	return setup.AdvancedMatchAny
}

func connectorRow(connector *widget.RadioGroup) fyne.CanvasObject {
	return container.NewHBox(
		widget.NewLabel("Combine with next group:"),
		connector,
	)
}
