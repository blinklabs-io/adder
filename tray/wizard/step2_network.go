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
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
	ouroboros "github.com/blinklabs-io/gouroboros"
)

type networkStep struct {
	ctx  context.Context
	plan *setup.SetupPlan

	radio       *widget.RadioGroup
	descLabel   *widget.Label
	customCheck *widget.Check
	address     *widget.Entry
	port        *widget.Entry
	testBtn     *widget.Button
	testResult  *widget.RichText
	advanced    *fyne.Container

	// testDone, when non-nil, is closed after the async testConnection
	// goroutine completes its fyne.Do callback. Tests use this instead of
	// polling widget state to avoid data races with the Fyne test driver.
	testDone chan struct{}
}

func (s *networkStep) Title() string { return "Select Network" }
func (s *networkStep) Description() string {
	return "Choose which Cardano network you want to monitor."
}

func (s *networkStep) Content() fyne.CanvasObject {
	if s.radio != nil {
		return container.NewVBox(
			container.NewHBox(
				widget.NewIcon(theme.ComputerIcon()),
				widget.NewLabel("Network Selection"),
			),
			widget.NewSeparator(),
			s.radio,
			container.NewPadded(s.descLabel),
			widget.NewSeparator(),
			s.customCheck,
			s.advanced,
		)
	}

	// Descriptions for networks
	networkDescs := map[string]string{
		"Mainnet": "The main Cardano production network. Recommended " +
			"for most users.",
		"Preprod": "A long-lived testnet for testing production-ready " +
			"applications. Matches Mainnet parameters.",
		"Preview": "A fast-paced testnet for testing new features and " +
			"upcoming hard forks.",
	}

	s.descLabel = widget.NewLabel(networkDescs["Mainnet"])
	s.descLabel.Wrapping = fyne.TextWrapWord

	s.radio = widget.NewRadioGroup(
		[]string{"Mainnet", "Preprod", "Preview"},
		func(selected string) {
			s.descLabel.SetText(networkDescs[selected])
		},
	)

	// Set initial selection from state
	initialNet := "Mainnet"
	if s.plan != nil && s.plan.Network.Name != "" {
		initialNet = strings.ToUpper(s.plan.Network.Name[:1]) +
			s.plan.Network.Name[1:]
	}
	s.radio.SetSelected(initialNet)
	s.descLabel.SetText(networkDescs[initialNet])

	// Advanced section
	s.address = widget.NewEntry()
	s.address.SetPlaceHolder("e.g. back-node.mainnet.example.com")
	if s.plan != nil {
		s.address.SetText(s.plan.Network.CustomAddress)
	}

	s.port = widget.NewEntry()
	s.port.SetPlaceHolder("3001")
	if s.plan != nil && s.plan.Network.CustomPort != 0 {
		s.port.SetText(strconv.Itoa(int(s.plan.Network.CustomPort)))
	}

	s.testResult = widget.NewRichText()
	s.testBtn = widget.NewButton("Test Connection", s.testConnection)

	advancedForm := widget.NewForm(
		widget.NewFormItem("Node Address", s.address),
		widget.NewFormItem("Node Port", s.port),
	)

	s.advanced = container.NewVBox(
		advancedForm,
		s.testBtn,
		s.testResult,
	)

	s.customCheck = widget.NewCheck(
		"Advanced: use custom node",
		func(checked bool) {
			if checked {
				s.advanced.Show()
			} else {
				s.advanced.Hide()
			}
		},
	)
	if s.plan != nil && s.plan.Network.CustomAddress != "" {
		s.customCheck.SetChecked(true)
		s.advanced.Show()
	} else {
		s.advanced.Hide()
	}

	return container.NewVBox(
		container.NewHBox(
			widget.NewIcon(theme.ComputerIcon()),
			widget.NewLabel("Network Selection"),
		),
		widget.NewSeparator(),
		s.radio,
		container.NewPadded(s.descLabel),
		widget.NewSeparator(),
		s.customCheck,
		s.advanced,
	)
}

func (s *networkStep) setTestStatus(msg string, isError bool, highlight string) {
	col := theme.ColorNameForeground
	if msg != "Testing connection..." {
		if isError {
			col = theme.ColorNameError
		} else {
			col = theme.ColorNameSuccess
		}
	}

	segments := []widget.RichTextSegment{
		&widget.TextSegment{
			Text: msg,
			Style: widget.RichTextStyle{
				ColorName: col,
			},
		},
	}
	if highlight != "" {
		segments = append(segments, &widget.TextSegment{
			Text: highlight,
			Style: widget.RichTextStyle{
				ColorName: col,
				TextStyle: fyne.TextStyle{Bold: true, Italic: true},
			},
		})
	}

	s.testResult.Segments = segments
	s.testResult.Refresh()
}

func (s *networkStep) testConnection() {
	s.setTestStatus("Testing connection...", false, "")
	s.testBtn.Disable()

	addr := s.address.Text
	port := s.port.Text

	// If not custom, get defaults from gouroboros
	if !s.customCheck.Checked {
		networkName := "mainnet"
		switch s.radio.Selected {
		case "Preprod":
			networkName = "preprod"
		case "Preview":
			networkName = "preview"
		}
		network, ok := ouroboros.NetworkByName(networkName)
		if ok && len(network.BootstrapPeers) > 0 {
			addr = network.BootstrapPeers[0].Address
			port = strconv.Itoa(int(network.BootstrapPeers[0].Port))
		} else if !ok {
			s.setTestStatus("Error: Unknown network: "+networkName, true, "")
			s.testBtn.Enable()
			return
		} else {
			s.setTestStatus(
				"Error: No bootstrap peers for "+networkName,
				true,
				"",
			)
			s.testBtn.Enable()
			return
		}
	} else {
		if addr == "" || port == "" {
			s.setTestStatus(
				"Error: Address and port required for custom node test.",
				true,
				"",
			)
			s.testBtn.Enable()
			return
		}
	}

	done := s.testDone
	go func() {
		target := net.JoinHostPort(addr, port)
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.DialContext(s.ctx, "tcp", target)

		fyne.Do(func() {
			// Check context again inside UI thread before updating
			if s.ctx.Err() != nil {
				if err == nil {
					_ = conn.Close()
				}
				return
			}

			s.testBtn.Enable()
			if err != nil {
				s.setTestStatus("Error! "+err.Error(), true, "")
			} else {
				_ = conn.Close()
				s.setTestStatus("Success! Connected to ", false, target)
			}
		})

		if done != nil {
			close(done)
		}
	}()
}

func (s *networkStep) Validate() error {
	if s.customCheck.Checked {
		if s.address.Text == "" {
			return errors.New("node address is required for custom configuration")
		}
		if s.port.Text == "" {
			return errors.New("port number is required for custom configuration")
		}
		p, err := strconv.Atoi(s.port.Text)
		if err != nil || p < 1 || p > 65535 {
			return errors.New("invalid port number (must be 1-65535)")
		}
	}
	return nil
}

func (s *networkStep) Apply(plan *setup.SetupPlan) {
	networkName := "mainnet"
	switch s.radio.Selected {
	case "Preprod":
		networkName = "preprod"
	case "Preview":
		networkName = "preview"
	}
	plan.Network.Name = networkName

	if s.customCheck.Checked {
		plan.Network.CustomAddress = s.address.Text
		p, _ := strconv.Atoi(s.port.Text)
		plan.Network.CustomPort = uint(p)
	} else {
		plan.Network.CustomAddress = ""
		plan.Network.CustomPort = 0
	}
}
