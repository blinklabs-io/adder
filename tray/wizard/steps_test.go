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
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkStepValidateAndApply(t *testing.T) {
	test.NewApp()
	plan := &setup.SetupPlan{
		Network: setup.NetworkConfig{
			Name:          "preview",
			CustomAddress: "node.example.test",
			CustomPort:    3001,
		},
	}
	step := &networkStep{ctx: context.Background(), plan: plan}
	step.Content()
	step.Content()

	require.True(t, step.customCheck.Checked)
	assert.NoError(t, step.Validate())

	step.address.SetText("")
	assert.ErrorContains(t, step.Validate(), "node address")

	step.address.SetText("node.example.test")
	step.port.SetText("")
	assert.ErrorContains(t, step.Validate(), "port number")

	for _, value := range []string{"not-a-port", "0", "65536"} {
		step.port.SetText(value)
		assert.ErrorContains(t, step.Validate(), "invalid port number")
	}

	step.port.SetText("3002")
	assert.NoError(t, step.Validate())

	got := &setup.SetupPlan{}
	step.Apply(got)
	assert.Equal(t, "preview", got.Network.Name)
	assert.Equal(t, "node.example.test", got.Network.CustomAddress)
	assert.Equal(t, uint(3002), got.Network.CustomPort)

	step.customCheck.SetChecked(false)
	step.radio.SetSelected("Preprod")
	step.Apply(got)
	assert.Equal(t, "preprod", got.Network.Name)
	assert.Empty(t, got.Network.CustomAddress)
	assert.Zero(t, got.Network.CustomPort)
}

func TestNetworkStepConnectionStatusWithoutDialing(t *testing.T) {
	test.NewApp()
	step := &networkStep{
		ctx:  context.Background(),
		plan: &setup.SetupPlan{Network: setup.NetworkConfig{Name: "mainnet"}},
	}
	step.Content()

	step.setTestStatus("Success! Connected to ", false, "127.0.0.1:3001")
	require.Len(t, step.testResult.Segments, 2)

	step.customCheck.SetChecked(true)
	step.address.SetText("")
	step.port.SetText("")
	step.testConnection()

	require.NotEmpty(t, step.testResult.Segments)
	assert.Contains(t, step.testResult.String(), "Address and port required")
	assert.False(t, step.testBtn.Disabled())
}

func TestNetworkStepTestConnectionDialResults(t *testing.T) {
	test.NewApp()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	step := &networkStep{
		ctx:  context.Background(),
		plan: &setup.SetupPlan{Network: setup.NetworkConfig{Name: "mainnet"}},
	}
	step.Content()
	step.customCheck.SetChecked(true)
	step.address.SetText(host)
	step.port.SetText(port)
	done1 := make(chan struct{})
	step.testDone = done1
	step.testConnection()
	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("first testConnection timed out")
	}
	assert.False(t, step.testBtn.Disabled())
	assert.Equal(t, "Success! Connected to "+listener.Addr().String(), step.testResult.String())

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddr := closed.Addr().String()
	require.NoError(t, closed.Close())
	host, port, err = net.SplitHostPort(closedAddr)
	require.NoError(t, err)

	done2 := make(chan struct{})
	step.testDone = done2
	step.address.SetText(host)
	step.port.SetText(port)
	step.testConnection()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("second testConnection timed out")
	}
	assert.False(t, step.testBtn.Disabled())
	assert.True(t, strings.Contains(step.testResult.String(), "Error!"))
}

func TestTemplateStepValidateOutputsAndApply(t *testing.T) {
	test.NewApp()
	home := t.TempDir()
	t.Setenv("HOME", home)

	step := &templateStep{
		plan: &setup.SetupPlan{
			Filter: setup.FilterConfig{Template: "Monitor Everything"},
			Output: setup.OutputConfig{Config: make(map[string]string)},
		},
	}
	step.Content()
	step.Content()
	(&templateStep{}).onOutputChange("Webhook")

	assert.NoError(t, step.Validate())

	step.selectTemplate("Watch Wallet")
	step.templateParam.SetText("not-valid")
	assert.ErrorContains(t, step.Validate(), "invalid template parameter")

	step.selectTemplate("Monitor Pool")
	step.templateParam.SetText("pool1test")
	assert.NoError(t, step.Validate())

	step.selectTemplate("Watch Wallet")
	step.templateParam.SetText("addr1test")
	step.outputSelect.SetSelected("Webhook")
	assert.ErrorContains(t, step.Validate(), "webhook URL")
	step.webhookURL.SetText("https://example.com/hook")
	step.webhookFormat.SetSelected("discord")
	assert.NoError(t, step.Validate())

	got := &setup.SetupPlan{}
	step.Apply(got)
	assert.Equal(t, "Watch Wallet", got.Filter.Template)
	assert.Equal(t, "addr1test", got.Filter.Param)
	assert.Equal(t, "webhook", got.Output.Type)
	assert.Equal(t, "https://example.com/hook", got.Output.Config["url"])
	assert.Equal(t, "discord", got.Output.Config["format"])

	step.outputSelect.SetSelected("Telegram")
	assert.ErrorContains(t, step.Validate(), "telegram token")
	step.telegramToken.SetText("token")
	step.telegramChat.SetText("@chat")
	assert.NoError(t, step.Validate())

	step.Apply(got)
	assert.Equal(t, "telegram", got.Output.Type)
	assert.Equal(t, "token", got.Output.Config["token"])
	assert.Equal(t, "@chat", got.Output.Config["chat_id"])

	step.outputSelect.SetSelected("Log to File")
	assert.ErrorContains(t, step.Validate(), "log file path")

	step.logFilePath.SetText(filepath.Join(home, "missing", "adder.log"))
	assert.ErrorContains(t, step.Validate(), "directory does not exist")

	step.logFilePath.SetText(home)
	assert.ErrorContains(t, step.Validate(), "log path must be a file")

	step.logFilePath.SetText("~/adder.log")
	assert.NoError(t, step.Validate())

	step.Apply(got)
	assert.Equal(t, "log", got.Output.Type)
	assert.Equal(t, filepath.Join(home, "adder.log"), got.Output.Config["path"])
	assert.Equal(t, "json", got.Output.Config["format"])

	step.outputSelect.SetSelected("None (desktop notifications only)")
	step.Apply(got)
	assert.Equal(t, "none", got.Output.Type)
}

func TestTemplateStepInitialOutputValues(t *testing.T) {
	test.NewApp()

	tests := []struct {
		name       string
		outputType string
		config     map[string]string
		selected   string
	}{
		{
			name:       "webhook",
			outputType: "webhook",
			config: map[string]string{
				"url":    "https://example.com/hook",
				"format": "discord",
			},
			selected: "Webhook",
		},
		{
			name:       "telegram",
			outputType: "telegram",
			config: map[string]string{
				"token":   "token",
				"chat_id": "@chat",
			},
			selected: "Telegram",
		},
		{
			name:       "log",
			outputType: "log",
			config:     map[string]string{"path": "/tmp/adder.log"},
			selected:   "Log to File",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step := &templateStep{
				plan: &setup.SetupPlan{
					Filter: setup.FilterConfig{
						Template: "Track DRep",
						Param:    "drep1test",
					},
					Output: setup.OutputConfig{
						Type:   tc.outputType,
						Config: tc.config,
					},
				},
			}
			step.Content()

			assert.Equal(t, tc.selected, step.outputSelect.Selected)
			assert.Equal(t, "Track DRep", step.selectedTemplate)
			assert.Equal(t, "drep1test", step.templateParam.Text)
		})
	}
}

func TestNotificationsStepLabelsAndApply(t *testing.T) {
	test.NewApp()

	tests := []struct {
		template string
		want     []string
	}{
		{
			template: "Monitor Everything",
			want: []string{
				setup.NotifyPrefBlocksMinted,
				setup.NotifyPrefIncomingTx,
				setup.NotifyPrefVotesCast,
			},
		},
		{
			template: "Watch Wallet",
			want: []string{
				setup.NotifyPrefIncomingTx,
				setup.NotifyPrefOutgoingTx,
				setup.NotifyPrefTokenTransfers,
			},
		},
		{
			template: "Track DRep",
			want: []string{
				setup.NotifyPrefGovProposals,
				setup.NotifyPrefVotesCast,
				setup.NotifyPrefRegChanges,
			},
		},
		{
			template: "Monitor Pool",
			want: []string{
				setup.NotifyPrefBlocksMinted,
				setup.NotifyPrefPoolParams,
			},
		},
		{template: "Unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.template, func(t *testing.T) {
			step := &notificationsStep{
				plan: &setup.SetupPlan{
					Filter: setup.FilterConfig{Template: tc.template},
					Output: setup.OutputConfig{Type: "webhook"},
					Notify: setup.NotificationPrefs{
						setup.NotifyPrefIncomingTx:       false,
						setup.NotifyPrefConnectionIssues: false,
					},
				},
			}
			assert.Equal(t, tc.want, step.getCheckLabels())

			step.Content()
			assert.Equal(t, len(tc.want), len(step.checks))
			if check, ok := step.checks[setup.NotifyPrefIncomingTx]; ok {
				assert.False(t, check.Checked)
				check.SetChecked(true)
			}
			step.connection.SetChecked(true)

			got := &setup.SetupPlan{Notify: make(setup.NotificationPrefs)}
			step.Apply(got)
			assert.True(t, got.Notify[setup.NotifyPrefConnectionIssues])
			if _, ok := step.checks[setup.NotifyPrefIncomingTx]; ok {
				assert.True(t, got.Notify[setup.NotifyPrefIncomingTx])
			}
		})
	}
}

func TestNotificationsStepVerificationButtons(t *testing.T) {
	test.NewApp()
	step := &notificationsStep{
		plan: &setup.SetupPlan{
			Filter: setup.FilterConfig{Template: "Monitor Everything"},
			Notify: make(setup.NotificationPrefs),
		},
	}
	step.Content()

	noButton := step.verifyBox.Objects[1].(*widget.Button)
	noButton.OnTapped()
	assert.False(t, step.verified)
	assert.Contains(t, step.verifyResult.Text, "Please check")

	yesButton := step.verifyBox.Objects[0].(*widget.Button)
	yesButton.OnTapped()
	assert.True(t, step.verified)
	assert.Contains(t, step.verifyResult.Text, "verified")
	assert.False(t, step.verifyBox.Visible())
}

func TestWelcomeStepApply(t *testing.T) {
	(&welcomeStep{}).Apply(&setup.SetupPlan{})
}

func TestWizardControllerCloseAndEnableButtons(t *testing.T) {
	test.NewApp()
	w := NewWizard(nil, nil)

	w.nextBtn.Disable()
	w.backBtn.Disable()
	w.current = 1
	w.EnableButtons()
	assert.False(t, w.nextBtn.Disabled())
	assert.False(t, w.backBtn.Disabled())

	w.Close()
	assert.ErrorIs(t, w.ctx.Err(), context.Canceled)
}

func TestShowWizardCreatesWindow(t *testing.T) {
	test.NewApp()
	ShowWizard(&setup.SetupPlan{
		API:    setup.APIConfig{Address: "127.0.0.1", Port: 8080},
		Output: setup.OutputConfig{Config: make(map[string]string)},
		Notify: make(setup.NotificationPrefs),
	}, nil)

	assert.NotNil(t, test.Canvas())
}

func TestNotificationsValidateDarwinVerified(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only branch")
	}
	step := &notificationsStep{verified: true}
	assert.NoError(t, step.Validate())
}
