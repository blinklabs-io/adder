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
	assert.Equal(
		t,
		"Success! Connected to "+listener.Addr().String(),
		step.testResult.String(),
	)

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
			Filter: setup.FilterConfig{MonitorEverything: true},
			Output: setup.OutputConfig{Config: make(map[string]string)},
		},
	}
	step.Content()
	step.Content() // idempotent re-render reuses the same widgets
	(&templateStep{}).onOutputChange("Webhook")

	// MonitorEverything is on → validation passes without targets.
	assert.NoError(t, step.Validate())

	// Turn it off → at least one target now required.
	step.everythingCheck.SetChecked(false)
	assert.ErrorContains(t, step.Validate(),
		"add at least one wallet, DRep, pool, asset, or policy")

	// Add a wallet; per-target validation rejects nonsense.
	step.wallets.entry.SetText("not-an-address")
	step.wallets.add(step.wallets.entry.Text)
	assert.True(t, step.wallets.errLabel.Visible())
	assert.Empty(t, step.wallets.values)

	// Pasting a pool ID into the Wallets section surfaces the
	// cross-template hint instead of a generic format error.
	step.wallets.entry.SetText("pool1xyz")
	step.wallets.add(step.wallets.entry.Text)
	assert.Contains(t, step.wallets.errLabel.Text,
		"did you mean to pick \"Monitor Pool\"")
	assert.Empty(t, step.wallets.values)

	// Valid wallet entry is accepted; the entry field is cleared.
	step.wallets.entry.SetText("addr1test")
	step.wallets.add(step.wallets.entry.Text)
	assert.Equal(t, []string{"addr1test"}, step.wallets.values)
	assert.False(t, step.wallets.errLabel.Visible())
	assert.Equal(t, "", step.wallets.entry.Text)

	// Output destination validations are unchanged.
	step.outputSelect.SetSelected("Webhook")
	assert.ErrorContains(t, step.Validate(), "webhook URL")
	step.webhookURL.SetText("https://example.com/hook")
	step.webhookFormat.SetSelected("discord")
	assert.NoError(t, step.Validate())

	got := &setup.SetupPlan{}
	step.Apply(got)
	assert.False(t, got.Filter.MonitorEverything)
	assert.Equal(t, []string{"addr1test"}, got.Filter.Wallets)
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
	assert.Equal(t, filepath.Join(home, "adder.log"),
		got.Output.Config["path"])
	assert.Equal(t, "json", got.Output.Config["format"])

	step.outputSelect.SetSelected("None (desktop notifications only)")
	step.Apply(got)
	assert.Equal(t, "none", got.Output.Type)
}

// TestTemplateStepAddRemoveAndSummary exercises the per-section add /
// remove flow and verifies the summary label updates after every
// mutation.
func TestTemplateStepAddRemoveAndSummary(t *testing.T) {
	test.NewApp()
	step := &templateStep{
		plan: &setup.SetupPlan{
			Output: setup.OutputConfig{Config: map[string]string{}},
		},
	}
	step.Content()

	// Initially: nothing configured, MonitorEverything off.
	assert.Equal(t, "Current configuration: No monitoring targets configured",
		step.summaryLabel.Text)

	// Add two wallets, one DRep, one pool.
	for _, v := range []string{"addr1a", "stake1b"} {
		step.wallets.entry.SetText(v)
		step.wallets.add(step.wallets.entry.Text)
	}
	step.dreps.entry.SetText("drep1abc")
	step.dreps.add(step.dreps.entry.Text)
	step.pools.entry.SetText("pool1abc")
	step.pools.add(step.pools.entry.Text)

	assert.Equal(t,
		"Current configuration: Standard: "+
			"2 wallets OR 1 DRep OR 1 pool",
		step.summaryLabel.Text)

	// Remove one wallet via the section's bookkeeping.
	step.wallets.removeValue("stake1b", step.wallets.list.Objects[1])
	assert.Equal(t, []string{"addr1a"}, step.wallets.values)
	assert.Equal(t,
		"Current configuration: Standard: "+
			"1 wallet OR 1 DRep OR 1 pool",
		step.summaryLabel.Text)

	// Toggling MonitorEverything on hides the summary detail; the
	// section values are preserved so toggling off restores them.
	step.everythingCheck.SetChecked(true)
	assert.Equal(t, "Current configuration: Monitor everything",
		step.summaryLabel.Text)
	assert.Equal(t, []string{"addr1a"}, step.wallets.values,
		"toggling Monitor Everything must preserve the lists")

	step.everythingCheck.SetChecked(false)
	assert.Equal(t,
		"Current configuration: Standard: "+
			"1 wallet OR 1 DRep OR 1 pool",
		step.summaryLabel.Text)

	// Apply with MonitorEverything off persists the lists; Apply with
	// it on clears them.
	got := &setup.SetupPlan{}
	step.Apply(got)
	assert.False(t, got.Filter.MonitorEverything)
	assert.Equal(t, []string{"addr1a"}, got.Filter.Wallets)
	assert.Equal(t, []string{"drep1abc"}, got.Filter.DReps)
	assert.Equal(t, []string{"pool1abc"}, got.Filter.Pools)

	step.everythingCheck.SetChecked(true)
	step.Apply(got)
	assert.True(t, got.Filter.MonitorEverything)
	assert.Empty(t, got.Filter.Wallets)
	assert.Empty(t, got.Filter.DReps)
	assert.Empty(t, got.Filter.Pools)
}

func TestTemplateStepAppliesTargetConnectors(t *testing.T) {
	test.NewApp()
	step := &templateStep{
		plan: &setup.SetupPlan{
			Filter: setup.FilterConfig{
				Wallets:   []string{"addr1abc"},
				DReps:     []string{"drep1abc"},
				DRepMatch: setup.AdvancedMatchAll,
			},
			Output: setup.OutputConfig{Config: map[string]string{}},
		},
	}
	step.Content()

	assert.Equal(t, connectorAndLabel, step.drepConnector.Selected)
	step.poolConnector.SetSelected(connectorAndLabel)
	got := &setup.SetupPlan{}
	step.Apply(got)
	assert.Equal(t, setup.AdvancedMatchAll, got.Filter.DRepMatch)
	assert.Equal(t, setup.AdvancedMatchAll, got.Filter.PoolMatch)
}

// TestTemplateStepAddSplitsCSV is the regression guard for the silent
// under-notification bug where a user pastes a comma-separated list
// (the pre-rewrite wizard's documented format) into a section. The
// validator's prefix check accepts the whole string, but it never
// matches in the engine because the asset set is keyed on individual
// fingerprints. After the fix, a CSV paste lands as multiple rows,
// each individually validated.
func TestTemplateStepAddSplitsCSV(t *testing.T) {
	test.NewApp()
	step := &templateStep{
		plan: &setup.SetupPlan{
			Output: setup.OutputConfig{Config: map[string]string{}},
		},
	}
	step.Content()

	step.assets.entry.SetText("asset1abc, asset1def,asset1ghi")
	step.assets.add(step.assets.entry.Text)
	assert.Equal(t,
		[]string{"asset1abc", "asset1def", "asset1ghi"},
		step.assets.values,
		"CSV paste must land as separate rows")
	assert.False(t, step.assets.errLabel.Visible())
	assert.Empty(t, step.assets.entry.Text,
		"entry is cleared on successful add")

	// One bad piece rejects the whole add — all-or-nothing.
	step.policies.entry.SetText(
		"5d0d3c1bf0a3c7c0e98d96aab6f1a3c7c0e98d96aab6f1a3c7c0e98d9," + // 56-hex valid
			"not-a-policy",
	)
	step.policies.add(step.policies.entry.Text)
	assert.True(t, step.policies.errLabel.Visible(),
		"a bad piece must fail validation")
	assert.Empty(t, step.policies.values,
		"all-or-nothing: nothing is added when any piece fails")
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
						DReps: []string{"drep1test"},
					},
					Output: setup.OutputConfig{
						Type:   tc.outputType,
						Config: tc.config,
					},
				},
			}
			step.Content()

			assert.Equal(t, tc.selected, step.outputSelect.Selected)
			// Step re-hydrates the DRep section from the persisted
			// plan so a returning user sees their previous targets.
			assert.Equal(t, []string{"drep1test"}, step.dreps.values)
			assert.Empty(t, step.wallets.values)
			assert.Empty(t, step.pools.values)
		})
	}
}

func TestNotificationsStepLabelsAndApply(t *testing.T) {
	test.NewApp()

	tests := []struct {
		name   string
		filter setup.FilterConfig
		want   []string
	}{
		{
			name:   "Monitor Everything",
			filter: setup.FilterConfig{MonitorEverything: true},
			want: []string{
				setup.NotifyPrefBlocksMinted,
				setup.NotifyPrefIncomingTx,
				setup.NotifyPrefVotesCast,
			},
		},
		{
			name:   "wallets only",
			filter: setup.FilterConfig{Wallets: []string{"a"}},
			want: []string{
				setup.NotifyPrefIncomingTx,
				setup.NotifyPrefOutgoingTx,
				setup.NotifyPrefTokenTransfers,
			},
		},
		{
			name:   "dreps only",
			filter: setup.FilterConfig{DReps: []string{"d"}},
			want: []string{
				setup.NotifyPrefGovProposals,
				setup.NotifyPrefVotesCast,
				setup.NotifyPrefRegChanges,
			},
		},
		{
			name:   "pools only",
			filter: setup.FilterConfig{Pools: []string{"p"}},
			want: []string{
				setup.NotifyPrefBlocksMinted,
				setup.NotifyPrefPoolParams,
			},
		},
		{
			// Combined: union of wallet + drep + pool prefs, in
			// order, no duplicates.
			name: "combined",
			filter: setup.FilterConfig{
				Wallets: []string{"a"},
				DReps:   []string{"d"},
				Pools:   []string{"p"},
			},
			want: []string{
				setup.NotifyPrefIncomingTx,
				setup.NotifyPrefOutgoingTx,
				setup.NotifyPrefTokenTransfers,
				setup.NotifyPrefGovProposals,
				setup.NotifyPrefVotesCast,
				setup.NotifyPrefRegChanges,
				setup.NotifyPrefBlocksMinted,
				setup.NotifyPrefPoolParams,
			},
		},
		{name: "empty plan", filter: setup.FilterConfig{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step := &notificationsStep{
				plan: &setup.SetupPlan{
					Filter: tc.filter,
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

// TestNotificationsStepApplyOverwritesStalePrefs is the regression
// guard for the multi-target wizard: when a user reconfigures from one
// target set (e.g. "Watch Wallet") to another (e.g. DRep-only), step 4
// shows a different label set, and Apply must rebuild plan.Notify from
// scratch so the labels that are no longer displayed do not survive in
// the persisted YAML. Without this, the inline dispatcher in
// tray/app.go keeps firing for tx events the user no longer cares
// about.
func TestNotificationsStepApplyOverwritesStalePrefs(t *testing.T) {
	test.NewApp()
	// Plan was last saved when the user had "Watch Wallet" configured,
	// so plan.Notify carries the wallet-related toggles AS WELL AS the
	// connection toggle. The user is now reconfiguring to DRep-only.
	plan := &setup.SetupPlan{
		Filter: setup.FilterConfig{DReps: []string{"drep1abc"}},
		Notify: setup.NotificationPrefs{
			// Stale, from a prior "Watch Wallet" run.
			setup.NotifyPrefIncomingTx:     true,
			setup.NotifyPrefOutgoingTx:     true,
			setup.NotifyPrefTokenTransfers: true,
			// Stale, from a prior "Monitor Pool" run.
			setup.NotifyPrefBlocksMinted: true,
			setup.NotifyPrefPoolParams:   true,
			// Still relevant.
			setup.NotifyPrefConnectionIssues: true,
		},
	}
	step := &notificationsStep{plan: plan}
	step.Content() // builds the gov-only check set for DRep-only

	step.Apply(plan)

	// After Apply, plan.Notify must contain ONLY the labels step 4
	// actually displayed for a DRep-only plan, plus the always-on
	// connection toggle. The wallet/pool keys must be gone.
	wantKeys := map[string]bool{
		setup.NotifyPrefGovProposals:     true,
		setup.NotifyPrefVotesCast:        true,
		setup.NotifyPrefRegChanges:       true,
		setup.NotifyPrefConnectionIssues: true,
	}
	gotKeys := map[string]bool{}
	for k := range plan.Notify {
		gotKeys[k] = true
	}
	assert.Equal(t, wantKeys, gotKeys,
		"stale prefs from prior wizard runs must not survive Apply")
}

// TestNotificationsStepApplyPreservesExplicitNo guards the
// review-feedback regression: a user who explicitly turned OFF a pref
// under one template must keep that "no" answer when they switch to a
// template where step 4 no longer renders the toggle. Otherwise their
// disabled alerts silently re-enable on the round trip.
func TestNotificationsStepApplyPreservesExplicitNo(t *testing.T) {
	test.NewApp()
	// Plan was last saved while configuring a wallet, with OutgoingTx
	// explicitly turned off. The user is now reconfiguring to
	// DRep-only — step 4 no longer shows OutgoingTx.
	plan := &setup.SetupPlan{
		Filter: setup.FilterConfig{DReps: []string{"drep1abc"}},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:       true,  // stale TRUE → drop
			setup.NotifyPrefOutgoingTx:       false, // explicit NO → keep
			setup.NotifyPrefBlocksMinted:     true,  // stale TRUE → drop
			setup.NotifyPrefConnectionIssues: true,
		},
	}
	step := &notificationsStep{plan: plan}
	step.Content() // builds the gov-only check set

	step.Apply(plan)

	// Stale TRUE keys for prefs step 4 doesn't render must be gone.
	_, hasIncoming := plan.Notify[setup.NotifyPrefIncomingTx]
	assert.False(t, hasIncoming,
		"stale TRUE IncomingTx must be dropped on template switch")
	_, hasBlocks := plan.Notify[setup.NotifyPrefBlocksMinted]
	assert.False(t, hasBlocks,
		"stale TRUE BlocksMinted must be dropped on template switch")
	// Explicit FALSE for an off-display pref must be preserved so the
	// user's disabled-alert choice survives the round trip.
	v, hasOutgoing := plan.Notify[setup.NotifyPrefOutgoingTx]
	assert.True(t, hasOutgoing,
		"explicit FALSE OutgoingTx must be preserved")
	assert.False(t, v,
		"preserved OutgoingTx value must still be false")
	// Currently displayed prefs are present.
	for _, k := range []string{
		setup.NotifyPrefGovProposals,
		setup.NotifyPrefVotesCast,
		setup.NotifyPrefRegChanges,
		setup.NotifyPrefConnectionIssues,
	} {
		_, ok := plan.Notify[k]
		assert.True(t, ok, "currently shown pref %s should be set", k)
	}
}

// TestNotificationsStepAdvancedRateLimit covers the new Advanced
// accordion: blank entries leave the plan at zero (engine uses
// defaults), valid entries flow into plan.App, and unparseable entries
// fail Validate so the wizard does not move forward with garbage.
func TestNotificationsStepAdvancedRateLimit(t *testing.T) {
	test.NewApp()
	step := &notificationsStep{
		plan: &setup.SetupPlan{
			Filter: setup.FilterConfig{MonitorEverything: true},
			Notify: make(setup.NotificationPrefs),
		},
	}
	step.Content()
	require.NotNil(t, step.rateLimitEntry)
	require.NotNil(t, step.rateWindowEntry)

	// Blank entries → Apply clears the plan back to zero.
	got := &setup.SetupPlan{App: setup.AppConfig{
		NotifyRateLimit:  99,
		NotifyRateWindow: time.Minute,
	}}
	require.NoError(t, step.Validate())
	step.Apply(got)
	assert.Equal(t, 0, got.App.NotifyRateLimit)
	assert.Equal(t, time.Duration(0), got.App.NotifyRateWindow)

	// Valid values → Apply writes them through.
	step.rateLimitEntry.SetText("10")
	step.rateWindowEntry.SetText("30s")
	require.NoError(t, step.Validate())
	step.Apply(got)
	assert.Equal(t, 10, got.App.NotifyRateLimit)
	assert.Equal(t, 30*time.Second, got.App.NotifyRateWindow)

	// Unparseable values → Validate refuses, Apply is not called.
	step.rateLimitEntry.SetText("not-a-number")
	require.ErrorContains(t, step.Validate(), "integer")
	step.rateLimitEntry.SetText("10")
	step.rateWindowEntry.SetText("five seconds")
	require.ErrorContains(t, step.Validate(), "duration")
	step.rateWindowEntry.SetText("0s")
	require.ErrorContains(
		t, step.Validate(), "greater than zero")
}

// TestNotificationsStepAdvancedRateLimitHydratesFromPlan guards the
// reconfigure round-trip: previously persisted values must show up in
// the Advanced entries on next wizard open.
func TestNotificationsStepAdvancedRateLimitHydratesFromPlan(t *testing.T) {
	test.NewApp()
	step := &notificationsStep{
		plan: &setup.SetupPlan{
			Filter: setup.FilterConfig{MonitorEverything: true},
			Notify: make(setup.NotificationPrefs),
			App: setup.AppConfig{
				NotifyRateLimit:  7,
				NotifyRateWindow: 45 * time.Second,
			},
		},
	}
	step.Content()
	assert.Equal(t, "7", step.rateLimitEntry.Text,
		"limit must hydrate from plan.App.NotifyRateLimit")
	assert.Equal(t, "45s", step.rateWindowEntry.Text,
		"window must hydrate from plan.App.NotifyRateWindow")
}

func TestNotificationsStepVerificationButtons(t *testing.T) {
	test.NewApp()
	step := &notificationsStep{
		plan: &setup.SetupPlan{
			Filter: setup.FilterConfig{MonitorEverything: true},
			Notify: make(setup.NotificationPrefs),
		},
	}
	step.Content()

	noButton := step.verifyBox.Objects[1].(*widget.Button)
	noButton.OnTapped()
	assert.False(t, step.verified)
	assert.Contains(t, step.verifyResult.Text, "notification settings")

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
