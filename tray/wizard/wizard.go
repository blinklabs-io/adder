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
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/tray/setup"
)

// Step defines the interface that each wizard step must implement.
type Step interface {
	// Title returns the title of the step shown in the header.
	Title() string
	// Description returns a short helpful text for the step.
	Description() string
	// Content returns the UI components for the step.
	Content() fyne.CanvasObject
	// Validate checks if the current step's input is valid.
	Validate() error
	// Apply updates the shared SetupPlan with the step's data.
	Apply(plan *setup.SetupPlan)
}

// WizardController manages the state and navigation of the setup wizard.
type WizardController struct {
	app      fyne.App
	window   fyne.Window
	plan     *setup.SetupPlan
	steps    []Step
	current  int
	callback func(context.Context, setup.SetupPlan, *WizardController)

	ctx    context.Context
	cancel context.CancelFunc

	titleLabel  *widget.Label
	descLabel   *widget.Label
	stepContent *fyne.Container
	backBtn     *widget.Button
	nextBtn     *widget.Button
}

func (w *WizardController) updateStep() {
	step := w.steps[w.current]

	// Update header
	w.titleLabel.SetText(step.Title())
	w.descLabel.SetText(step.Description())

	// Update content
	w.stepContent.Objects = []fyne.CanvasObject{
		container.NewPadded(step.Content()),
	}
	w.stepContent.Refresh()

	// Update buttons
	if w.current == 0 {
		w.backBtn.Hide()
	} else {
		w.backBtn.Show()
	}

	if w.current == len(w.steps)-1 {
		w.nextBtn.SetText("Finish Setup")
		w.nextBtn.Importance = widget.HighImportance
		w.nextBtn.SetIcon(theme.ConfirmIcon())
	} else {
		w.nextBtn.SetText("Next Step")
		w.nextBtn.Importance = widget.MediumImportance
		w.nextBtn.SetIcon(theme.NavigateNextIcon())
	}
}

func (w *WizardController) nextStep() {
	step := w.steps[w.current]
	if err := step.Validate(); err != nil {
		slog.Warn("step validation failed", "step", step.Title(), "error", err)
		dialog.ShowError(err, w.window)
		return
	}
	step.Apply(w.plan)

	if w.current == len(w.steps)-1 {
		w.finish()
		return
	}

	w.current++
	w.updateStep()
}

func (w *WizardController) prevStep() {
	if w.current > 0 {
		w.current--
		w.updateStep()
	}
}

func (w *WizardController) finish() {
	slog.Info("wizard input complete",
		"network", w.plan.Network.Name,
		"monitoring", setup.SummarizeFilter(w.plan.Filter))

	w.nextBtn.Disable()
	w.backBtn.Disable()

	if w.callback != nil {
		w.callback(w.ctx, *w.plan, w)
	}
}

// Close closes the wizard window and cancels the internal context.
func (w *WizardController) Close() {
	w.cancel()
	w.window.Close()
}

// EnableButtons re-enables navigation buttons if a background task fails.
func (w *WizardController) EnableButtons() {
	w.nextBtn.Enable()
	if w.current > 0 {
		w.backBtn.Enable()
	}
}

// NewWizard creates a new wizard controller.
func NewWizard(
	initialPlan *setup.SetupPlan,
	callback func(context.Context, setup.SetupPlan, *WizardController),
) *WizardController {
	a := fyne.CurrentApp()
	if a == nil {
		a = app.New()
	}

	ctx, cancel := context.WithCancel(context.Background())

	var plan *setup.SetupPlan
	if initialPlan != nil {
		plan = initialPlan
	} else {
		plan = &setup.SetupPlan{
			API: setup.APIConfig{
				Address: "127.0.0.1",
				Port:    8080,
			},
			Output: setup.OutputConfig{
				Config: make(map[string]string),
			},
			Notify: make(setup.NotificationPrefs),
		}
	}

	w := &WizardController{
		app:      a,
		ctx:      ctx,
		plan:     plan,
		callback: callback,
		cancel:   cancel,
	}

	w.window = w.app.NewWindow("Adder Setup")
	w.window.Resize(fyne.NewSize(500, 560))
	w.window.SetFixedSize(true)
	w.window.SetOnClosed(w.cancel)

	w.steps = []Step{
		&welcomeStep{},
		&networkStep{ctx: ctx, plan: w.plan},
		&templateStep{plan: w.plan},
		&notificationsStep{plan: w.plan},
	}

	// Header elements
	w.titleLabel = widget.NewLabelWithStyle(
		"",
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)
	w.descLabel = widget.NewLabel("")
	w.descLabel.Wrapping = fyne.TextWrapWord

	w.stepContent = container.NewStack()

	w.backBtn = widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), w.prevStep)
	w.nextBtn = widget.NewButtonWithIcon("Next Step", theme.NavigateNextIcon(), w.nextStep)
	w.nextBtn.IconPlacement = widget.ButtonIconTrailingText

	header := container.NewVBox(
		container.NewPadded(container.NewVBox(
			w.titleLabel,
			w.descLabel,
		)),
	)

	footer := container.NewPadded(container.NewHBox(
		layout.NewSpacer(),
		w.backBtn,
		w.nextBtn,
	))

	mainLayout := container.NewBorder(header, footer, nil, nil, w.stepContent)

	w.window.SetContent(mainLayout)
	w.updateStep()
	return w
}

// ShowWizard launches the setup wizard.
func ShowWizard(
	initialPlan *setup.SetupPlan,
	callback func(context.Context, setup.SetupPlan, *WizardController),
) {
	slog.Debug("launching setup wizard")
	fyne.Do(func() {
		w := NewWizard(initialPlan, callback)
		w.window.CenterOnScreen()
		w.window.Show()
	})
}

// Wizard is an alias for WizardController for backward compatibility
type Wizard = WizardController
