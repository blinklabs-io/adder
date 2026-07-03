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
	"fmt"
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/require"
)

// TestCaptureSteps renders every wizard step at its natural full height
// (no scroll clipping, no fixed header to composite over) and writes one
// PNG per step for manual/visual inspection. Dev-only: skipped unless
// WIZARD_CAPTURE=1 so it never runs in CI. Output dir defaults to /tmp
// (override with WIZARD_CAPTURE_DIR); files are wizard_step1.png ... N.
func TestCaptureSteps(t *testing.T) {
	if os.Getenv("WIZARD_CAPTURE") != "1" {
		t.Skip("set WIZARD_CAPTURE=1 to render wizard steps to PNGs")
	}

	test.NewApp()
	w := NewWizard(nil, nil)

	// Populate step 3's target sections so its chips are visible in the
	// capture (matches the reported repro).
	s3 := w.steps[2].(*templateStep)
	s3.Content()
	s3.wallets.add(
		"addr1q9hlrf6lmtgu7mqupwlysw7qcvexmjkmwfynqlfh33dz87xy3y67g6" +
			"0shkajwfsewt2tjs85a3xkpkmcafpwwzpevlcsmwzj82",
	)
	s3.dreps.add("drep1y2cvruq6syfa4w7uksw9jur9q06lwlc60p9kjcxnxd9ww7gh8gtt0")
	s3.pools.add("pool1ws7gpqkw4wpdj33lf3hcjy9zk5pxr8htnnxkxepe49p5gp3srcg")

	dir := os.Getenv("WIZARD_CAPTURE_DIR")
	if dir == "" {
		dir = "/tmp"
	}

	for i, step := range w.steps {
		content := container.NewPadded(step.Content())

		// Height = the taller of the default window and the step's own
		// natural height, so nothing is clipped for tall steps.
		h := content.MinSize().Height
		if h < surfaceHeight {
			h = surfaceHeight
		}

		win := test.NewWindow(content)
		win.Resize(fyne.NewSize(surfaceWidth, h))
		win.Content().Refresh()

		path := fmt.Sprintf("%s/wizard_step%d.png", dir, i+1)
		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, png.Encode(f, win.Canvas().Capture()))
		require.NoError(t, f.Close())
		win.Close()
		t.Logf("wrote %s (%s)", path, step.Title())
	}
}
