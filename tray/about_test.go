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
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/blinklabs-io/adder/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatAboutVersion(t *testing.T) {
	origVer := version.Version
	origCommit := version.CommitHash
	defer func() {
		version.Version = origVer
		version.CommitHash = origCommit
	}()

	// Release version with commit hash
	version.Version = "v0.28.0"
	version.CommitHash = "5530eed"
	assert.Equal(t, "Version: v0.28.0 (commit: 5530eed)", formatAboutVersion())

	// Devel fallback with commit hash
	version.Version = ""
	version.CommitHash = "5530eed"
	assert.Equal(t, "Version: devel (commit: 5530eed)", formatAboutVersion())

	// Release version without commit hash
	version.Version = "v0.28.0"
	version.CommitHash = ""
	assert.Equal(t, "Version: v0.28.0", formatAboutVersion())
}

func TestShowAbout(t *testing.T) {
	origVer := version.Version
	origCommit := version.CommitHash
	defer func() {
		version.Version = origVer
		version.CommitHash = origCommit
	}()
	version.Version = "v1.2.3"
	version.CommitHash = "deadbeef"

	app := test.NewApp()
	win := ShowAbout(app)
	require.NotNil(t, win)
	assert.Equal(t, "About Adder", win.Title())

	content := win.Content()
	require.NotNil(t, content)

	verStr := formatAboutVersion()
	assert.Equal(t, "Version: v1.2.3 (commit: deadbeef)", verStr)

	img := win.Canvas().Capture()
	if f, err := os.Create(filepath.Join(t.TempDir(), "about_window.png")); err == nil {
		_ = png.Encode(f, img)
		_ = f.Close()
	}

	win.Close()
}

func TestAppShowAboutIdempotent(t *testing.T) {
	app := test.NewApp()
	a := &App{fyneApp: app}

	a.showAbout()
	require.NotNil(t, a.aboutWindow)
	firstWin := a.aboutWindow

	// Calling showAbout again should reuse the existing window
	a.showAbout()
	assert.Equal(t, firstWin, a.aboutWindow)

	// Closing it resets a.aboutWindow to nil
	a.aboutWindow.Close()
	assert.Nil(t, a.aboutWindow)
}
