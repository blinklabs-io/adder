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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/internal/version"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockUpdateChecker struct {
	info *ReleaseInfo
	err  error
}

func (m *mockUpdateChecker) CheckLatestRelease(
	ctx context.Context,
) (*ReleaseInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.info, nil
}

func TestNormalizeVersion(t *testing.T) {
	assert.Equal(t, "v0.44.0", normalizeVersion("0.44.0"))
	assert.Equal(t, "v0.44.0", normalizeVersion("v0.44.0"))
	assert.Equal(t, "v0.44.0", normalizeVersion("  v0.44.0  "))
	assert.Equal(t, "", normalizeVersion(""))
}

func TestIsUpdateAvailable(t *testing.T) {
	tests := []struct {
		name          string
		currentVer    string
		latestTag     string
		wantAvailable bool
		wantIsDev     bool
	}{
		{
			name:          "newer release available",
			currentVer:    "v0.43.0",
			latestTag:     "v0.44.0",
			wantAvailable: true,
			wantIsDev:     false,
		},
		{
			name:          "already on latest release",
			currentVer:    "v0.44.0",
			latestTag:     "v0.44.0",
			wantAvailable: false,
			wantIsDev:     false,
		},
		{
			name:          "running ahead of release",
			currentVer:    "v0.45.0",
			latestTag:     "v0.44.0",
			wantAvailable: false,
			wantIsDev:     false,
		},
		{
			name:          "without leading v prefix",
			currentVer:    "0.43.0",
			latestTag:     "v0.44.0",
			wantAvailable: true,
			wantIsDev:     false,
		},
		{
			name:          "devel version",
			currentVer:    "devel",
			latestTag:     "v0.44.0",
			wantAvailable: true,
			wantIsDev:     true,
		},
		{
			name:          "empty version",
			currentVer:    "",
			latestTag:     "v0.44.0",
			wantAvailable: true,
			wantIsDev:     true,
		},
		{
			name:          "dev prefix",
			currentVer:    "dev-build-1",
			latestTag:     "v0.44.0",
			wantAvailable: true,
			wantIsDev:     true,
		},
		{
			name:          "invalid latest tag",
			currentVer:    "v0.44.0",
			latestTag:     "not-semver",
			wantAvailable: false,
			wantIsDev:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			avail, isDev := IsUpdateAvailable(tt.currentVer, tt.latestTag)
			assert.Equal(t, tt.wantAvailable, avail)
			assert.Equal(t, tt.wantIsDev, isDev)
		})
	}
}

func TestFindAssetForPlatform(t *testing.T) {
	assets := []ReleaseAsset{
		{
			Name: "adder-0.44.0-darwin-amd64.pkg",
		},
		{
			Name: "adder-0.44.0-darwin-arm64.pkg",
		},
		{
			Name: "adder-v0.44.0-windows-amd64.msi",
		},
		{
			Name: "adder-v0.44.0-windows-arm64.msi",
		},
		{
			Name: "adder-v0.44.0-linux-amd64.tar.gz",
		},
		{
			Name: "adder-v0.44.0-linux-arm64.tar.gz",
		},
		{
			Name: "adder-v0.44.0-freebsd-amd64.tar.gz",
		},
	}

	darwinArm := FindAssetForPlatform(assets, "darwin", "arm64")
	require.NotNil(t, darwinArm)
	assert.Equal(t, "adder-0.44.0-darwin-arm64.pkg", darwinArm.Name)

	darwinAmd := FindAssetForPlatform(assets, "darwin", "amd64")
	require.NotNil(t, darwinAmd)
	assert.Equal(t, "adder-0.44.0-darwin-amd64.pkg", darwinAmd.Name)

	winAmd := FindAssetForPlatform(assets, "windows", "amd64")
	require.NotNil(t, winAmd)
	assert.Equal(t, "adder-v0.44.0-windows-amd64.msi", winAmd.Name)

	linuxAmd := FindAssetForPlatform(assets, "linux", "amd64")
	require.NotNil(t, linuxAmd)
	assert.Equal(t, "adder-v0.44.0-linux-amd64.tar.gz", linuxAmd.Name)

	freebsdAmd := FindAssetForPlatform(assets, "freebsd", "amd64")
	require.NotNil(t, freebsdAmd)
	assert.Equal(t, "adder-v0.44.0-freebsd-amd64.tar.gz", freebsdAmd.Name)

	unsupportedOS := FindAssetForPlatform(assets, "solaris", "amd64")
	assert.Nil(t, unsupportedOS)

	unsupportedArch := FindAssetForPlatform(assets, "freebsd", "riscv64")
	assert.Nil(t, unsupportedArch)

	// Ensure prefix matching like "arm" does not match "arm64"
	armMismatch := FindAssetForPlatform(assets, "linux", "arm")
	assert.Nil(t, armMismatch)

	// Add an explicit arm asset and verify it matches arm and not arm64
	assetsWithArm := append(assets, ReleaseAsset{
		Name: "adder-v0.44.0-linux-arm.tar.gz",
	})
	linuxArm := FindAssetForPlatform(assetsWithArm, "linux", "arm")
	require.NotNil(t, linuxArm)
	assert.Equal(t, "adder-v0.44.0-linux-arm.tar.gz", linuxArm.Name)
}

func TestGitHubUpdateChecker(t *testing.T) {
	expectedInfo := ReleaseInfo{
		TagName: "v0.44.0",
		Name:    "v0.44.0",
		HTMLURL: "https://github.com/blinklabs-io/adder/releases/tag/v0.44.0",
		Assets: []ReleaseAsset{
			{
				Name:               "adder-0.44.0-darwin-arm64.pkg",
				BrowserDownloadURL: "https://example.com/download.pkg",
				Size:               12345,
			},
		},
	}

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(expectedInfo)
		}),
	)
	defer server.Close()

	checker := &GitHubUpdateChecker{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	info, err := checker.CheckLatestRelease(context.Background())
	require.NoError(t, err)
	assert.Equal(t, expectedInfo.TagName, info.TagName)
	require.Len(t, info.Assets, 1)
	assert.Equal(t, "adder-0.44.0-darwin-arm64.pkg", info.Assets[0].Name)
}

func TestGitHubUpdateCheckerError(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	defer server.Close()

	checker := &GitHubUpdateChecker{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	_, err := checker.CheckLatestRelease(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestDownloadAsset(t *testing.T) {
	payload := []byte("fake binary payload content for testing")

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}),
	)
	defer server.Close()

	tmpDir, err := os.MkdirTemp("", "adder-test-download-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	destPath := filepath.Join(tmpDir, "test.pkg")
	payloadHash := sha256.Sum256(payload)
	validDigest := "sha256:" + hex.EncodeToString(payloadHash[:])

	var progressCalled bool
	var totalDownloaded int64
	err = DownloadAsset(
		context.Background(),
		server.Client(),
		server.URL,
		destPath,
		validDigest,
		func(downloaded, total int64) {
			progressCalled = true
			totalDownloaded = downloaded
		},
	)
	require.NoError(t, err)
	assert.True(t, progressCalled)
	assert.Equal(t, int64(len(payload)), totalDownloaded)

	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, payload, data)
}

func TestDownloadAsset_DigestMismatch(t *testing.T) {
	payload := []byte("binary installer test data")
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}),
	)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.pkg")

	badDigest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	err := DownloadAsset(
		context.Background(),
		server.Client(),
		server.URL,
		destPath,
		badDigest,
		nil,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum verification failed")
	assert.NoFileExists(t, destPath)
}

func TestDownloadAsset_Truncated(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("short"))
		}),
	)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.pkg")

	err := DownloadAsset(
		context.Background(),
		server.Client(),
		server.URL,
		destPath,
		"",
		nil,
	)
	require.Error(t, err)
	assert.True(
		t,
		strings.Contains(err.Error(), "download incomplete") ||
			strings.Contains(err.Error(), "unexpected EOF"),
	)
	assert.NoFileExists(t, destPath)
}

func TestDownloadAssetCancel(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			// Slowly write to allow cancel to trigger
			for range 10 {
				_, _ = w.Write([]byte("chunk"))
				w.(http.Flusher).Flush()
				time.Sleep(20 * time.Millisecond)
			}
		}),
	)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.pkg")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := DownloadAsset(
		ctx,
		server.Client(),
		server.URL,
		destPath,
		"",
		nil,
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.NoFileExists(t, destPath)
	assert.NoFileExists(t, destPath+".part")
}

func TestShowUpdateWindow(t *testing.T) {
	origVer := version.Version
	defer func() {
		version.Version = origVer
	}()
	version.Version = "v0.44.0"

	checker := &mockUpdateChecker{
		info: &ReleaseInfo{
			TagName: "v0.44.0",
			Name:    "v0.44.0",
			HTMLURL: "https://github.com/blinklabs-io/adder/releases/tag/v0.44.0",
		},
	}

	done := make(chan struct{})
	origHook := onCheckDone
	onCheckDone = func() {
		close(done)
	}
	defer func() {
		onCheckDone = origHook
	}()

	app := test.NewApp()
	win := ShowUpdateWindow(app, checker)
	require.NotNil(t, win)
	assert.Equal(t, "Software Update", win.Title())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for check to complete")
	}

	win.Close()
}

func TestAppShowCheckForUpdatesIdempotent(t *testing.T) {
	checker := &mockUpdateChecker{
		info: &ReleaseInfo{
			TagName: "v0.44.0",
			Name:    "v0.44.0",
			HTMLURL: "https://github.com/blinklabs-io/adder/releases/tag/v0.44.0",
		},
	}

	done := make(chan struct{})
	origHook := onCheckDone
	onCheckDone = func() {
		close(done)
	}
	defer func() {
		onCheckDone = origHook
	}()

	app := test.NewApp()
	a := &App{
		fyneApp:       app,
		updateChecker: checker,
	}

	a.showCheckForUpdates()
	require.NotNil(t, a.updateWindow)
	firstWin := a.updateWindow

	// Second call should reuse the window
	a.showCheckForUpdates()
	assert.Equal(t, firstWin, a.updateWindow)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for check to complete")
	}

	// Closing resets reference to nil
	a.updateWindow.Close()
	assert.Nil(t, a.updateWindow)
}

func TestAppShowCheckForUpdatesWithOnSkipPersistsConfig(t *testing.T) {
	origVer := version.Version
	defer func() {
		version.Version = origVer
	}()
	version.Version = "v0.43.0"

	checker := &mockUpdateChecker{
		info: &ReleaseInfo{
			TagName: "v0.44.0",
			Name:    "v0.44.0",
			HTMLURL: "https://github.com/blinklabs-io/adder/releases/tag/v0.44.0",
		},
	}

	done := make(chan struct{})
	origHook := onCheckDone
	onCheckDone = func() {
		close(done)
	}
	defer func() {
		onCheckDone = origHook
	}()

	store := &setup.LocalStore{
		TrayConfigPath: filepath.Join(t.TempDir(), "adder-tray.yaml"),
	}
	app := test.NewApp()
	a := &App{
		fyneApp:       app,
		updateChecker: checker,
		runner: &setup.SetupRunner{
			Store: store,
		},
	}

	a.showCheckForUpdates()
	require.NotNil(t, a.updateWindow)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for check to complete")
	}

	var skipBtn *widget.Button
	require.Eventually(t, func() bool {
		fyne.Do(func() {
			skipBtn = findButton(a.updateWindow.Content(), "Skip This Version")
		})
		return skipBtn != nil
	}, 2*time.Second, 10*time.Millisecond)
	require.NotNil(t, skipBtn)
	fyne.Do(func() {
		skipBtn.OnTapped()
	})

	assert.Equal(t, "v0.44.0", a.Config().SkippedVersion)

	loaded, err := store.LoadTray()
	require.NoError(t, err)
	assert.Equal(t, "v0.44.0", loaded.SkippedVersion)
}

func TestShowUpdateWindow_UpdateAvailable(t *testing.T) {
	origVer := version.Version
	defer func() {
		version.Version = origVer
	}()
	version.Version = "v0.43.0"

	checker := &mockUpdateChecker{
		info: &ReleaseInfo{
			TagName: "v0.44.0",
			Name:    "v0.44.0",
			HTMLURL: "https://github.com/blinklabs-io/adder/releases/tag/v0.44.0",
			Body: `• Fixes a regression where cycle sizes were not being reset between corner and half actions.
• Fixes a bug with incorrect window width when cycling between displays.`,
			Assets: []ReleaseAsset{
				testHostAsset("https://github.com/blinklabs-io/adder/releases/download/v0.44.0"),
			},
		},
	}

	done := make(chan struct{})
	origHook := onCheckDone
	onCheckDone = func() {
		close(done)
	}
	defer func() {
		onCheckDone = origHook
	}()

	var skippedVersion string
	app := test.NewApp()
	win := ShowUpdateWindow(
		app,
		checker,
		WithOnSkip(func(ver string) {
			skippedVersion = ver
		}),
	)
	require.NotNil(t, win)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for check to complete")
	}

	var (
		cancelBtn  *widget.Button
		installBtn *widget.Button
		skipBtn    *widget.Button
		quitBtn    *widget.Button
	)
	require.Eventually(t, func() bool {
		fyne.Do(func() {
			quitBtn = findButton(win.Content(), "Install on Quit")
			cancelBtn = findButton(win.Content(), "Cancel")
			installBtn = findButton(win.Content(), "Install and Relaunch")
			skipBtn = findButton(win.Content(), "Skip This Version")
		})
		return installBtn != nil && skipBtn != nil && cancelBtn != nil
	}, 2*time.Second, 10*time.Millisecond)

	assert.Empty(t, skippedVersion)

	// Capture visual artifact for manual and tool inspection
	img := win.Canvas().Capture()
	f, err := os.Create(filepath.Join(t.TempDir(), "update_window.png"))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, img))

	assert.Nil(t, quitBtn)
	assert.NotNil(t, cancelBtn)
	assert.NotNil(t, installBtn)
	require.NotNil(t, skipBtn)
	fyne.Do(func() {
		skipBtn.OnTapped()
	})
	assert.Equal(t, "v0.44.0", skippedVersion)
}

func TestShowUpdateWindow_InstallAndRelaunch(t *testing.T) {
	origVer := version.Version
	origLauncher := installerLauncher
	defer func() {
		version.Version = origVer
		installerLauncher = origLauncher
	}()
	version.Version = "v0.43.0"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mock installer"))
	}))
	defer server.Close()

	launchedCh := make(chan string, 1)
	installerLauncher = func(path string) error {
		launchedCh <- path
		return nil
	}

	checker := &mockUpdateChecker{
		info: &ReleaseInfo{
			TagName: "v0.44.0",
			Name:    "v0.44.0",
			HTMLURL: "https://github.com/blinklabs-io/adder/releases/tag/v0.44.0",
			Body:    "* Test release notes",
			Assets: []ReleaseAsset{
				testHostAsset(server.URL),
			},
		},
	}

	done := make(chan struct{})
	origHook := onCheckDone
	onCheckDone = func() {
		close(done)
	}
	defer func() {
		onCheckDone = origHook
	}()

	relaunchedCh := make(chan struct{}, 1)
	app := test.NewApp()
	win := ShowUpdateWindow(
		app,
		checker,
		WithOnRelaunch(func() {
			relaunchedCh <- struct{}{}
		}),
	)
	require.NotNil(t, win)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for check to complete")
	}

	var relaunchBtn *widget.Button
	require.Eventually(t, func() bool {
		fyne.Do(func() {
			relaunchBtn = findButton(win.Content(), "Install and Relaunch")
		})
		return relaunchBtn != nil
	}, 2*time.Second, 10*time.Millisecond)
	require.NotNil(t, relaunchBtn)
	fyne.Do(func() {
		relaunchBtn.OnTapped()
	})

	select {
	case launchedPath := <-launchedCh:
		assert.FileExists(t, launchedPath)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for launcher")
	}

	var quitInstallBtn *widget.Button
	fyne.Do(func() {
		quitInstallBtn = findButton(win.Content(), "Quit & Install")
	})
	require.NotNil(t, quitInstallBtn)
	fyne.Do(func() {
		quitInstallBtn.OnTapped()
	})

	select {
	case <-relaunchedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relaunch callback")
	}
}

func findButton(obj fyne.CanvasObject, text string) *widget.Button {
	if btn, ok := obj.(*widget.Button); ok {
		if btn.Text == text {
			return btn
		}
	}
	if c, ok := obj.(*fyne.Container); ok {
		for _, child := range c.Objects {
			if res := findButton(child, text); res != nil {
				return res
			}
		}
	}
	return nil
}

func testHostAsset(baseURL string) ReleaseAsset {
	var ext string
	switch runtime.GOOS {
	case "darwin":
		ext = ".pkg"
	case "windows":
		ext = ".msi"
	default:
		ext = ".tar.gz"
	}
	return ReleaseAsset{
		Name:               fmt.Sprintf("adder_0.44.0_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext),
		BrowserDownloadURL: baseURL + "/test" + ext,
		Size:               14,
	}
}
