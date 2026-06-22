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

	"fyne.io/fyne/v2/test"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
)

func TestWizard_Navigation(t *testing.T) {
	test.NewApp()

	callbackCalled := false
	var finishedPlan setup.SetupPlan

	callback := func(
		ctx context.Context,
		plan setup.SetupPlan,
		w *WizardController,
	) {
		callbackCalled = true
		finishedPlan = plan
	}

	w := NewWizard(nil, callback)

	// Check initial state
	assert.Equal(t, 0, w.current)
	assert.False(t, w.backBtn.Visible())
	assert.Equal(t, "Next Step", w.nextBtn.Text)

	// Advance to Step 2 (Network)
	w.nextStep()
	assert.Equal(t, 1, w.current)
	assert.True(t, w.backBtn.Visible())

	// Advance to Step 3 (Template)
	w.nextStep()
	assert.Equal(t, 2, w.current)
	assert.Equal(t, "Next Step", w.nextBtn.Text)

	// Step 3 (Template) requires at least one target. Add a wallet
	// via the section's entry+Add path.
	s3 := w.steps[2].(*templateStep)
	s3.Content() // build widgets
	s3.wallets.entry.SetText("addr1qxy648m6k96350t4tql82q0e8sqpks54" +
		"uvlttclat4e0z6298lyp4578c7l655e09f8v7mwy5h653zls2nd335g" +
		"58xvsf2y066")
	s3.wallets.add(s3.wallets.entry.Text)

	// Advance to Step 4 (Notifications)
	w.nextStep()
	assert.Equal(t, 3, w.current)
	assert.Equal(t, "Finish Setup", w.nextBtn.Text)

	// Step 4 (Notifications) requires verification for validation
	s4 := w.steps[3].(*notificationsStep)
	s4.verified = true

	// Go back to Step 3
	w.prevStep()
	assert.Equal(t, 2, w.current)
	assert.Equal(t, "Next Step", w.nextBtn.Text)

	// Go forward again to Step 4
	w.nextStep()
	assert.Equal(t, 3, w.current)

	// Finish
	w.nextStep()
	assert.True(t, callbackCalled)
	assert.False(t, finishedPlan.Filter.MonitorEverything)
	assert.Len(t, finishedPlan.Filter.Wallets, 1)
}

func TestWizardPlan_Initial(t *testing.T) {
	test.NewApp()

	w := NewWizard(nil, nil)
	assert.Equal(t, "127.0.0.1", w.plan.API.Address)
	assert.Equal(t, uint(8080), w.plan.API.Port)
}

// TestNotificationsValidateAlwaysOptional guards that step 4 never
// blocks the wizard on the "Send Test Notification" button — the
// previous macOS-only gate (s.verified must be true) was removed
// because forcing it broke iteration during active development and
// added no real value: users who never see a notification can grant
// permission via System Settings without redoing setup.
func TestNotificationsValidateAlwaysOptional(t *testing.T) {
	step := &notificationsStep{}
	assert.NoError(t, step.Validate())
}
