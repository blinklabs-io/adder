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

package tray

import (
	"fmt"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/internal/ui/assets"
	"github.com/blinklabs-io/adder/internal/version"
)

// formatAboutVersion returns the version string formatted as
// "Version: <ver> (commit: <hash>)".
func formatAboutVersion() string {
	ver := version.Version
	if ver == "" {
		ver = "devel"
	}
	if version.CommitHash != "" {
		return fmt.Sprintf("Version: %s (commit: %s)", ver, version.CommitHash)
	}
	return "Version: " + ver
}

// ShowAbout creates and displays the About dialog window showing the
// current release version, description, and project links.
func ShowAbout(fyneApp fyne.App) fyne.Window {
	win := fyneApp.NewWindow("About Adder")

	icon := canvas.NewImageFromResource(assets.GetIcon(64, nil))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(64, 64))
	iconBox := container.NewCenter(icon)

	title := widget.NewLabelWithStyle(
		"Adder",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	versionLabel := widget.NewLabelWithStyle(
		formatAboutVersion(),
		fyne.TextAlignCenter,
		fyne.TextStyle{},
	)

	desc := widget.NewLabelWithStyle(
		"Cardano blockchain event tailing & notification daemon",
		fyne.TextAlignCenter,
		fyne.TextStyle{Italic: true},
	)

	repoURL, _ := url.Parse("https://github.com/blinklabs-io/adder")
	repoLink := widget.NewHyperlink(
		"https://github.com/blinklabs-io/adder",
		repoURL,
	)
	repoLink.Alignment = fyne.TextAlignCenter

	licenseLabel := widget.NewLabelWithStyle(
		"Apache-2.0 License | Blink Labs Software",
		fyne.TextAlignCenter,
		fyne.TextStyle{},
	)

	closeBtn := widget.NewButton("Close", func() {
		win.Close()
	})
	closeBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		iconBox,
		title,
		versionLabel,
		desc,
		repoLink,
		licenseLabel,
		layout.NewSpacer(),
		container.NewCenter(closeBtn),
	)

	win.SetContent(container.NewPadded(content))
	win.Resize(fyne.NewSize(380, 280))
	win.CenterOnScreen()
	win.Show()

	return win
}
