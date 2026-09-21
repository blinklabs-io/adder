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
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/blinklabs-io/adder/internal/ui/assets"
	"github.com/blinklabs-io/adder/internal/version"
	"golang.org/x/mod/semver"
)

const defaultReleaseRepo = "blinklabs-io/adder"

// ReleaseAsset represents an artifact asset attached to a GitHub release.
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

// ReleaseInfo represents the GitHub release payload.
type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	HTMLURL     string         `json:"html_url"`
	Body        string         `json:"body"`
	PublishedAt string         `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

// UpdateChecker retrieves latest release metadata from a remote source.
type UpdateChecker interface {
	CheckLatestRelease(ctx context.Context) (*ReleaseInfo, error)
}

// GitHubUpdateChecker queries GitHub releases API for latest published release.
type GitHubUpdateChecker struct {
	Repo       string
	BaseURL    string
	HTTPClient *http.Client
}

// NewGitHubUpdateChecker returns a configured GitHubUpdateChecker.
func NewGitHubUpdateChecker(repo string) *GitHubUpdateChecker {
	if repo == "" {
		repo = defaultReleaseRepo
	}
	return &GitHubUpdateChecker{
		Repo: repo,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// CheckLatestRelease fetches the latest release from GitHub.
func (c *GitHubUpdateChecker) CheckLatestRelease(
	ctx context.Context,
) (*ReleaseInfo, error) {
	apiURL := c.BaseURL
	if apiURL == "" {
		apiURL = fmt.Sprintf(
			"https://api.github.com/repos/%s/releases/latest",
			c.Repo,
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating update request: %w", err)
	}

	v := version.Version
	if v == "" {
		v = "devel"
	}
	req.Header.Set("User-Agent", "adder-tray/"+v)
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyStr := strings.TrimSpace(string(bodyBytes))
		if bodyStr != "" {
			return nil, fmt.Errorf("release check failed: HTTP %d: %s", resp.StatusCode, bodyStr)
		}
		return nil, fmt.Errorf("release check failed: HTTP %d", resp.StatusCode)
	}

	var info ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding release payload: %w", err)
	}

	return &info, nil
}

// normalizeVersion ensures the version string has a 'v' prefix for semver
// operations.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// IsUpdateAvailable compares the current version string against the latest
// release tag. It returns whether an update is available and whether the
// running binary is an unversioned/development build.
func IsUpdateAvailable(currentVer, latestTag string) (available bool, isDev bool) {
	normLatest := normalizeVersion(latestTag)
	if !semver.IsValid(normLatest) {
		return false, false
	}

	trimmed := strings.TrimSpace(currentVer)
	if trimmed == "" || trimmed == "devel" || strings.HasPrefix(trimmed, "dev") {
		return true, true
	}

	normCurrent := normalizeVersion(trimmed)
	if !semver.IsValid(normCurrent) {
		return true, true
	}

	return semver.Compare(normLatest, normCurrent) > 0, false
}

// FindAssetForPlatform matches a release installer asset for the target OS and
// architecture. Only platform package installers (.pkg on darwin, .msi on
// windows) are returned. Platforms that only distribute tarballs (Linux,
// FreeBSD) return nil so the application directs the user to the release page.
func FindAssetForPlatform(
	assets []ReleaseAsset,
	goos, goarch string,
) *ReleaseAsset {
	var expectedExt string
	switch goos {
	case "darwin":
		expectedExt = ".pkg"
	case "windows":
		expectedExt = ".msi"
	default:
		return nil
	}

	targetOS := strings.ToLower(goos)
	targetArch := strings.ToLower(goarch)

	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		if strings.HasSuffix(name, expectedExt) &&
			strings.Contains(name, targetOS) &&
			containsArchToken(name, targetArch) {
			return &assets[i]
		}
	}
	return nil
}

func containsArchToken(name, arch string) bool {
	if arch == "" {
		return false
	}
	for idx := 0; ; {
		pos := strings.Index(name[idx:], arch)
		if pos < 0 {
			return false
		}
		start := idx + pos
		end := start + len(arch)
		beforeOK := start == 0 || !isAlphaNum(name[start-1])
		afterOK := end == len(name) || !isAlphaNum(name[end])
		if beforeOK && afterOK {
			return true
		}
		idx = start + 1
	}
}

func isAlphaNum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func normalizeDigest(digest string) string {
	return strings.TrimPrefix(
		strings.ToLower(strings.TrimSpace(digest)),
		"sha256:",
	)
}

func isValidSHA256Digest(digest string) bool {
	hexStr := normalizeDigest(digest)
	if len(hexStr) != 64 {
		return false
	}
	for i := 0; i < len(hexStr); i++ {
		c := hexStr[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// DownloadAsset downloads a release asset to destPath with progress reporting
// and optional checksum integrity verification.
func DownloadAsset(
	ctx context.Context,
	client *http.Client,
	downloadURL string,
	destPath string,
	expectedDigest string,
	progress func(downloaded, total int64),
) error {
	if expectedDigest != "" && !isValidSHA256Digest(expectedDigest) {
		return fmt.Errorf("invalid SHA-256 digest format: %q", expectedDigest)
	}

	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		downloadURL,
		nil,
	)
	if err != nil {
		return fmt.Errorf("creating download request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		bodyStr := strings.TrimSpace(string(bodyBytes))
		if bodyStr != "" {
			return fmt.Errorf("download failed: HTTP %d: %s", resp.StatusCode, bodyStr)
		}
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("creating target directory: %w", err)
	}

	partPath := destPath + ".part"
	_ = os.Remove(partPath)
	file, err := os.OpenFile(
		partPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("creating temporary download file: %w", err)
	}

	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(partPath)
	}

	total := resp.ContentLength
	buf := make([]byte, 64*1024)
	var downloaded int64
	var lastReport time.Time
	hasher := sha256.New()
	mw := io.MultiWriter(file, hasher)

	for {
		select {
		case <-ctx.Done():
			cleanup()
			return ctx.Err()
		default:
		}

		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := mw.Write(buf[:n]); werr != nil {
				cleanup()
				return fmt.Errorf("writing download data: %w", werr)
			}
			downloaded += int64(n)
			if progress != nil &&
				(time.Since(lastReport) > 100*time.Millisecond ||
					downloaded == total) {
				progress(downloaded, total)
				lastReport = time.Now()
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			cleanup()
			return fmt.Errorf("reading download stream: %w", rerr)
		}
	}

	if total > 0 && downloaded != total {
		cleanup()
		return fmt.Errorf(
			"download incomplete: received %d of %d bytes",
			downloaded,
			total,
		)
	}

	if expectedDigest != "" {
		expectedHash := normalizeDigest(expectedDigest)
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if expectedHash != actualHash {
			cleanup()
			return fmt.Errorf(
				"checksum verification failed: expected sha256:%s, got sha256:%s",
				expectedHash,
				actualHash,
			)
		}
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("closing downloaded file: %w", err)
	}

	if err := os.Rename(partPath, destPath); err != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("finalizing downloaded file: %w", err)
	}

	return nil
}

// installerLauncher handles launching the platform installer.
var installerLauncher = func(filePath string) error {
	switch runtime.GOOS {
	case "darwin":
		// #nosec G204 -- filePath is a downloaded installer verified by checksum
		return exec.Command("open", filePath).Start()
	case "windows":
		// #nosec G204 -- filePath is a downloaded installer verified by checksum
		return exec.Command("msiexec.exe", "/i", filePath).Start()
	default:
		return fmt.Errorf("automatic installation is not supported on %s", runtime.GOOS)
	}
}

// onCheckDone is an internal hook called when the background release check finishes.
// Used by tests to synchronize before asserting UI state or tearing down windows.
var onCheckDone func()

// UpdateWindowOption configures optional behavior for ShowUpdateWindow.
type UpdateWindowOption func(*updateWindowConfig)

type updateWindowConfig struct {
	onRelaunch     func()
	onSkip         func(version string)
	onClosed       func()
	targetPlatform [2]string
	checkWeekly    bool
	onCheckWeekly  func(bool)
}

// WithOnRelaunch sets the callback invoked when the user confirms relaunching
// after an update has downloaded and the installer has launched.
func WithOnRelaunch(fn func()) UpdateWindowOption {
	return func(c *updateWindowConfig) {
		c.onRelaunch = fn
	}
}

// WithOnSkip sets the callback invoked when the user chooses to skip a release.
func WithOnSkip(fn func(version string)) UpdateWindowOption {
	return func(c *updateWindowConfig) {
		c.onSkip = fn
	}
}

// WithOnClosed sets the callback invoked after the update window is closed.
func WithOnClosed(fn func()) UpdateWindowOption {
	return func(c *updateWindowConfig) {
		c.onClosed = fn
	}
}

// WithCheckWeekly sets the initial state and callback for the weekly update
// check preference in the update window.
func WithCheckWeekly(initial bool, onToggle func(bool)) UpdateWindowOption {
	return func(c *updateWindowConfig) {
		c.checkWeekly = initial
		c.onCheckWeekly = onToggle
	}
}

// ShowUpdateWindow displays a Sparkle-style update checking window.
func ShowUpdateWindow(
	fyneApp fyne.App,
	checker UpdateChecker,
	opts ...UpdateWindowOption,
) fyne.Window {
	var cfg updateWindowConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	if checker == nil {
		checker = NewGitHubUpdateChecker("")
	}

	win := fyneApp.NewWindow("Software Update")
	ctx, cancel := context.WithCancel(context.Background())
	win.SetOnClosed(func() {
		cancel()
		if cfg.onClosed != nil {
			cfg.onClosed()
		}
	})

	icon := canvas.NewImageFromResource(assets.GetFullResource())
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(64, 64))

	titleLabel := widget.NewLabelWithStyle(
		"Software Update",
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)

	subtitleLabel := widget.NewLabel("Checking for updates…")
	subtitleLabel.Wrapping = fyne.TextWrapWord

	headerText := container.NewVBox(titleLabel, subtitleLabel)
	header := container.NewBorder(
		nil,
		nil,
		container.NewCenter(icon),
		nil,
		container.NewPadded(headerText),
	)

	boxBg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	boxBg.CornerRadius = 6
	boxBg.StrokeWidth = 1
	boxBg.StrokeColor = theme.Color(theme.ColorNameSeparator)

	notesRichText := widget.NewRichText()
	notesRichText.Wrapping = fyne.TextWrapWord
	scrollNotes := container.NewScroll(notesRichText)
	scrollNotes.SetMinSize(fyne.NewSize(520, 140))

	defaultURL, _ := url.Parse("https://github.com/" + defaultReleaseRepo + "/releases")
	detailsLink := widget.NewHyperlink("Details", defaultURL)
	detailsLink.Alignment = fyne.TextAlignLeading
	historyLink := widget.NewHyperlink("Recent version history", defaultURL)
	historyLink.Alignment = fyne.TextAlignLeading
	linksBox := container.NewVBox(detailsLink, historyLink)

	boxContent := container.NewBorder(nil, linksBox, nil, nil, scrollNotes)
	framedBox := container.NewStack(boxBg, container.NewPadded(boxContent))
	framedBox.Hide()

	progressBar := widget.NewProgressBar()
	progressLabel := widget.NewLabelWithStyle(
		"",
		fyne.TextAlignCenter,
		fyne.TextStyle{Italic: true},
	)
	progressBox := container.NewVBox(progressBar, progressLabel)
	progressBox.Hide()

	skipBtn := widget.NewButton("Skip This Version", nil)
	skipBtn.Hide()

	installBtn := widget.NewButton("Install and Relaunch", nil)
	installBtn.Importance = widget.HighImportance
	installBtn.Hide()

	closeBtn := widget.NewButton("Cancel", func() {
		cancel()
		win.Close()
	})

	checkWeekly := widget.NewCheck("Check for updates weekly", nil)
	checkWeekly.Checked = cfg.checkWeekly
	checkWeekly.OnChanged = func(checked bool) {
		if cfg.onCheckWeekly != nil {
			cfg.onCheckWeekly(checked)
		}
	}
	checkWeekly.Hide()

	buttonBox := container.NewHBox(
		skipBtn,
		layout.NewSpacer(),
		closeBtn,
		installBtn,
	)

	bodyStack := container.NewStack(framedBox, progressBox)
	bottomBox := container.NewVBox(checkWeekly, buttonBox)

	content := container.NewBorder(
		header,
		bottomBox,
		nil,
		nil,
		container.NewPadded(bodyStack),
	)

	win.SetContent(container.NewPadded(content))
	win.Resize(fyne.NewSize(580, 440))
	win.CenterOnScreen()
	win.Show()

	var runCheck func()
	runCheck = func() {
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			titleLabel.SetText("Software Update")
			subtitleLabel.SetText("Checking for updates…")
			framedBox.Hide()
			progressBox.Hide()
			checkWeekly.Hide()
			skipBtn.Hide()
			installBtn.Hide()
			closeBtn.SetText("Cancel")
			closeBtn.OnTapped = func() {
				cancel()
				win.Close()
			}
			closeBtn.Show()
			buttonBox.Refresh()
			content.Refresh()
		})

		go func() {
			defer func() {
				if onCheckDone != nil {
					onCheckDone()
				}
			}()

			info, err := checker.CheckLatestRelease(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return
				}
				slog.Error("update check failed", "error", err)
				fyne.Do(func() {
					if ctx.Err() != nil {
						return
					}
					titleLabel.SetText("Update Error")
					subtitleLabel.SetText(
						"Unable to check for updates: " + err.Error(),
					)
					framedBox.Hide()
					progressBox.Hide()
					checkWeekly.Hide()
					skipBtn.Hide()
					installBtn.SetText("Retry")
					installBtn.Importance = widget.MediumImportance
					installBtn.OnTapped = runCheck
					installBtn.Show()
					closeBtn.SetText("Cancel")
					closeBtn.Show()
					buttonBox.Refresh()
					content.Refresh()
				})
				return
			}

			if ctx.Err() != nil {
				return
			}

			curVer := version.Version
			if curVer == "" {
				curVer = "devel"
			}

			available, isDev := IsUpdateAvailable(curVer, info.TagName)
			if !available {
				fyne.Do(func() {
					if ctx.Err() != nil {
						return
					}
					titleLabel.SetText("You're up to date!")
					subtitleLabel.SetText(fmt.Sprintf(
						"Adder %s is currently the newest version available.",
						curVer,
					))
					framedBox.Hide()
					progressBox.Hide()
					checkWeekly.Show()
					skipBtn.Hide()
					installBtn.Hide()
					closeBtn.SetText("OK")
					closeBtn.OnTapped = func() { win.Close() }
					closeBtn.Show()
					buttonBox.Refresh()
					content.Refresh()
				})
				return
			}

			notesText := strings.TrimSpace(info.Body)
			if notesText == "" {
				notesText = "* Improvements and bug fixes."
			}
			lines := strings.Split(notesText, "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "•") {
					lines[i] = "* " + strings.TrimSpace(strings.TrimPrefix(trimmed, "•"))
				}
			}
			notesText = strings.Join(lines, "\n")

			targetOS := runtime.GOOS
			targetArch := runtime.GOARCH
			if cfg.targetPlatform[0] != "" {
				targetOS = cfg.targetPlatform[0]
				targetArch = cfg.targetPlatform[1]
			}
			asset := FindAssetForPlatform(info.Assets, targetOS, targetArch)

			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				richMarkdown := widget.NewRichTextFromMarkdown(notesText)
				notesRichText.Segments = richMarkdown.Segments
				notesRichText.Refresh()

				if parsedURL, uerr := url.Parse(info.HTMLURL); uerr == nil {
					detailsLink.SetURL(parsedURL)
				}
				histURL, _ := url.Parse("https://github.com/" + defaultReleaseRepo + "/releases")
				historyLink.SetURL(histURL)

				if isDev {
					titleLabel.SetText("A new version of Adder is available!")
					subtitleLabel.SetText(fmt.Sprintf(
						"Development build %s detected. Latest release is %s. Would you like to install it and relaunch Adder now?",
						curVer,
						info.TagName,
					))
				} else {
					titleLabel.SetText("A new version of Adder is available!")
					subtitleLabel.SetText(fmt.Sprintf(
						"Adder %s is now available (you have %s). Would you like to install it and relaunch Adder now?",
						info.TagName,
						curVer,
					))
				}

				framedBox.Show()
				progressBox.Hide()
				checkWeekly.Show()

				skipBtn.OnTapped = func() {
					if cfg.onSkip != nil {
						cfg.onSkip(info.TagName)
					}
					win.Close()
				}
				skipBtn.Show()

				if asset == nil {
					subtitleLabel.SetText(fmt.Sprintf(
						"Adder %s is now available (you have %s). Automatic installation is not supported on your platform. You can download and install the update manually.",
						info.TagName,
						curVer,
					))
					installBtn.SetText("Open Release Page")
					installBtn.Importance = widget.HighImportance
					installBtn.OnTapped = func() {
						if u, err := url.Parse(info.HTMLURL); err == nil {
							_ = fyneApp.OpenURL(u)
						}
						win.Close()
					}
					installBtn.Show()
					closeBtn.SetText("Cancel")
					closeBtn.Importance = widget.MediumImportance
					closeBtn.OnTapped = func() { win.Close() }
					closeBtn.Show()
					buttonBox.Refresh()
					content.Refresh()
					return
				}

				var startDownload func()
				startDownload = func() {
					if !isValidSHA256Digest(asset.Digest) {
						slog.Error("missing or unsupported checksum digest for update asset", "asset", asset.Name, "digest", asset.Digest)
						fyne.Do(func() {
							titleLabel.SetText("Update Verification Failed")
							subtitleLabel.SetText("The release asset does not have a valid SHA-256 digest.")
							checkWeekly.Hide()
							closeBtn.SetText("Close")
							closeBtn.Importance = widget.MediumImportance
							closeBtn.OnTapped = func() { win.Close() }
							closeBtn.Show()
							buttonBox.Refresh()
							content.Refresh()
						})
						return
					}

					assetName := filepath.Base(asset.Name)
					if assetName == "." || assetName == ".." ||
						assetName != asset.Name ||
						strings.ContainsAny(asset.Name, `/\`) {
						slog.Error("rejecting unsafe asset name", "name", asset.Name)
						fyne.Do(func() {
							titleLabel.SetText("Download Failed")
							subtitleLabel.SetText("The release asset name is invalid.")
						})
						return
					}
					downloadDir, mkErr := os.MkdirTemp("", "adder-update-")
					if mkErr != nil {
						slog.Error("creating download directory", "error", mkErr)
						fyne.Do(func() {
							titleLabel.SetText("Download Failed")
							subtitleLabel.SetText("Could not create secure temporary directory.")
						})
						return
					}
					targetFile := filepath.Join(downloadDir, assetName)

					dlCtx, dlCancel := context.WithCancel(ctx)
					fyne.Do(func() {
						titleLabel.SetText(fmt.Sprintf("Downloading Adder %s…", info.TagName))
						subtitleLabel.SetText("Please wait while the update is downloaded…")
						framedBox.Hide()
						progressBox.Show()
						checkWeekly.Hide()
						skipBtn.Hide()
						installBtn.Hide()
						progressBar.SetValue(0)
						progressLabel.SetText("Connecting…")
						closeBtn.SetText("Cancel")
						closeBtn.Importance = widget.MediumImportance
						closeBtn.OnTapped = func() {
							dlCancel()
							runCheck()
						}
						closeBtn.Show()
						buttonBox.Refresh()
						content.Refresh()
					})

					go func() {
						dlErr := DownloadAsset(
							dlCtx,
							nil,
							asset.BrowserDownloadURL,
							targetFile,
							asset.Digest,
							func(downloaded, total int64) {
								fyne.Do(func() {
									if total > 0 {
										ratio := float64(downloaded) / float64(total)
										progressBar.SetValue(ratio)
										progressLabel.SetText(fmt.Sprintf(
											"%.1f MB / %.1f MB (%.0f%%)",
											float64(downloaded)/(1024*1024),
											float64(total)/(1024*1024),
											ratio*100,
										))
									} else {
										progressLabel.SetText(fmt.Sprintf(
											"%.1f MB downloaded",
											float64(downloaded)/(1024*1024),
										))
									}
								})
							},
						)
						if dlErr != nil {
							if errors.Is(dlErr, context.Canceled) {
								return
							}
							slog.Error("failed to download update", "error", dlErr)
							fyne.Do(func() {
								titleLabel.SetText("Download Failed")
								subtitleLabel.SetText(dlErr.Error())
								progressBox.Hide()
								checkWeekly.Hide()
								closeBtn.SetText("Close")
								closeBtn.Importance = widget.MediumImportance
								closeBtn.OnTapped = func() { win.Close() }
								installBtn.SetText("Retry")
								installBtn.Importance = widget.MediumImportance
								installBtn.OnTapped = func() { startDownload() }
								installBtn.Show()
								buttonBox.Refresh()
								content.Refresh()
							})
							return
						}

						if dlCtx.Err() != nil || ctx.Err() != nil {
							return
						}

						fyne.Do(func() {
							if dlCtx.Err() != nil || ctx.Err() != nil {
								return
							}
							titleLabel.SetText("A new version of Adder is ready to install!")
							subtitleLabel.SetText(fmt.Sprintf(
								"Adder %s has been downloaded and the installer was launched. Adder will now quit to complete installation.",
								info.TagName,
							))
							progressBox.Hide()
							closeBtn.SetText("Quit & Install")
							closeBtn.Importance = widget.HighImportance
							closeBtn.OnTapped = func() {
								win.Close()
								if cfg.onRelaunch != nil {
									cfg.onRelaunch()
								} else {
									fyneApp.Quit()
								}
							}
							closeBtn.Show()
							buttonBox.Refresh()
							content.Refresh()

							if dlCtx.Err() != nil || ctx.Err() != nil {
								return
							}
							if lerr := installerLauncher(targetFile); lerr != nil {
								slog.Error("failed to launch installer", "error", lerr)
								titleLabel.SetText("Failed to launch installer")
								subtitleLabel.SetText(fmt.Sprintf(
									"Downloaded to: %s\nError: %s",
									targetFile,
									lerr.Error(),
								))
								closeBtn.SetText("Close")
								closeBtn.Importance = widget.MediumImportance
								closeBtn.OnTapped = func() { win.Close() }
							}
						})
					}()
				}

				installBtn.SetText("Install and Relaunch")
				installBtn.Importance = widget.HighImportance
				installBtn.OnTapped = func() {
					startDownload()
				}
				installBtn.Show()

				closeBtn.SetText("Cancel")
				closeBtn.Importance = widget.MediumImportance
				closeBtn.OnTapped = func() { win.Close() }
				closeBtn.Show()

				buttonBox.Refresh()
				content.Refresh()
			})
		}()
	}

	runCheck()
	return win
}
