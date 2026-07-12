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

package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWalletAddr(t *testing.T) {
	tests := []struct {
		name      string
		param     string
		wantError string
	}{
		{name: "payment address", param: "addr1test"},
		{name: "stake address", param: "stake1test"},
		{
			name:      "empty rejected",
			param:     "",
			wantError: "must not be empty",
		},
		{
			name:      "DRep input → cross-template hint",
			param:     "drep1test",
			wantError: "did you mean to pick \"Track DRep\"",
		},
		{
			name:      "pool input → cross-template hint",
			param:     "pool1test",
			wantError: "did you mean to pick \"Monitor Pool\"",
		},
		{
			name:      "truly unknown prefix",
			param:     "xyz1test",
			wantError: "invalid address",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWalletAddr(tc.param)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateDRepID(t *testing.T) {
	tests := []struct {
		name      string
		param     string
		wantError string
	}{
		{name: "bech32", param: "drep1test"},
		{name: "hex", param: "deadbeef"},
		{
			// Regression: hex.DecodeString("") returns nil, so the
			// validator must reject "" explicitly before falling
			// through to the hex check.
			name:      "empty rejected",
			param:     "",
			wantError: "must not be empty",
		},
		{
			name:      "pool input → cross-template hint",
			param:     "pool1w7c2j0px43jmudhf48ezp7dy8j7904c9l3wc7809lhh2z026hch",
			wantError: "did you mean to pick \"Monitor Pool\"",
		},
		{
			name:      "wallet input → cross-template hint",
			param:     "addr1xyz",
			wantError: "did you mean to pick \"Watch Wallet\"",
		},
		{
			name:      "non hex",
			param:     "not-hex",
			wantError: "invalid DRep ID",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDRepID(tc.param)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidatePoolID(t *testing.T) {
	tests := []struct {
		name      string
		param     string
		wantError string
	}{
		{name: "bech32", param: "pool1test"},
		{name: "hex", param: "feedface"},
		{
			name:      "empty rejected",
			param:     "",
			wantError: "must not be empty",
		},
		{
			name:      "DRep input → cross-template hint",
			param:     "drep1abc",
			wantError: "did you mean to pick \"Track DRep\"",
		},
		{
			name:      "wallet input → cross-template hint",
			param:     "stake1xyz",
			wantError: "did you mean to pick \"Watch Wallet\"",
		},
		{
			name:      "non hex",
			param:     "not-hex",
			wantError: "invalid Pool ID",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePoolID(tc.param)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateAssetFingerprint(t *testing.T) {
	tests := []struct {
		name      string
		param     string
		wantError string
	}{
		{name: "bech32", param: "asset1abcxyz"},
		{
			name:      "empty rejected",
			param:     "",
			wantError: "must not be empty",
		},
		{
			name:      "DRep input → cross-template hint",
			param:     "drep1abc",
			wantError: "did you mean to pick \"Track DRep\"",
		},
		{
			name:      "pool input → cross-template hint",
			param:     "pool1abc",
			wantError: "did you mean to pick \"Monitor Pool\"",
		},
		{
			name:      "wallet input → cross-template hint",
			param:     "addr1xyz",
			wantError: "did you mean to pick \"Watch Wallet\"",
		},
		{
			// Plain hex without an asset1 prefix gets rejected: hex
			// alone is ambiguous (could be a policy or arbitrary
			// data) and the cardano fingerprint convention is the
			// bech32 form.
			name:      "plain hex rejected",
			param:     "deadbeef",
			wantError: "invalid asset fingerprint",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAssetFingerprint(tc.param)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidatePolicyID(t *testing.T) {
	tests := []struct {
		name      string
		param     string
		wantError string
	}{
		{
			// 28 bytes = 56 hex chars.
			name:  "valid 56-char hex",
			param: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef00",
		},
		{
			name:      "empty rejected",
			param:     "",
			wantError: "must not be empty",
		},
		{
			name:      "wrong length hex rejected",
			param:     "deadbeef",
			wantError: "invalid policy ID",
		},
		{
			name:      "right length non-hex rejected",
			param:     "zzcdef0123456789abcdef0123456789abcdef0123456789abcdef00",
			wantError: "invalid policy ID",
		},
		{
			name:      "asset input → cross-template hint",
			param:     "asset1xyz",
			wantError: "did you mean to pick \"Follow Asset\"",
		},
		{
			name:      "DRep input → cross-template hint",
			param:     "drep1abc",
			wantError: "did you mean to pick \"Track DRep\"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePolicyID(tc.param)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestSummarizeFilter(t *testing.T) {
	cases := []struct {
		name   string
		filter FilterConfig
		want   string
	}{
		{"empty", FilterConfig{}, "nothing configured"},
		{
			"everything",
			FilterConfig{MonitorEverything: true},
			"everything",
		},
		{
			"single wallet",
			FilterConfig{Wallets: []string{"addr1"}},
			"1 wallet",
		},
		{
			"two wallets",
			FilterConfig{Wallets: []string{"a", "b"}},
			"2 wallets",
		},
		{
			"combined",
			FilterConfig{
				Wallets: []string{"a", "b"},
				DReps:   []string{"d"},
				Pools:   []string{"p1", "p2", "p3"},
			},
			"2 wallets, 1 DRep, 3 pools",
		},
		{
			"single asset",
			FilterConfig{Assets: []string{"asset1"}},
			"1 asset",
		},
		{
			"two policies",
			FilterConfig{Policies: []string{"a", "b"}},
			"2 policies",
		},
		{
			// All five kinds populated — the order is wallet → DRep
			// → pool → asset → policy and matches the wizard's
			// section order so the summary line reads naturally.
			"all five kinds",
			FilterConfig{
				Wallets:  []string{"a"},
				DReps:    []string{"d"},
				Pools:    []string{"p"},
				Assets:   []string{"x", "y"},
				Policies: []string{"q"},
			},
			"1 wallet, 1 DRep, 1 pool, 2 assets, 1 policy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SummarizeFilter(tc.filter))
		})
	}
}

func TestPathsUseOverridesAndExpandTilde(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	logDir := filepath.Join(t.TempDir(), "logs")
	t.Setenv("ADDER_TRAY_CONFIG_DIR", configDir)
	t.Setenv("ADDER_TRAY_LOG_DIR", logDir)

	assert.Equal(t, configDir, ConfigDir())
	assert.Equal(t, logDir, LogDir())

	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir reads USERPROFILE on Windows, HOME elsewhere.
	t.Setenv("USERPROFILE", home)

	got, err := ExpandTildePath("~/adder.log")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "adder.log"), got)

	got, err = ExpandTildePath("~")
	require.NoError(t, err)
	assert.Equal(t, home, got)

	got, err = ExpandTildePath("~other/adder.log")
	require.NoError(t, err)
	assert.Equal(t, "~other/adder.log", got)

	got, err = ExpandTildePath("/tmp/adder.log")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/adder.log", got)
}

func TestPlatformPathsAreAbsolute(t *testing.T) {
	t.Setenv("ADDER_TRAY_CONFIG_DIR", "")
	t.Setenv("ADDER_TRAY_LOG_DIR", "")
	t.Setenv("HOME", t.TempDir())

	assert.True(t, filepath.IsAbs(ConfigDir()))
	assert.True(t, filepath.IsAbs(LogDir()))
}

func TestAppBinaryFinderUsesCurrentWorkingDirectory(t *testing.T) {
	name := "adder"
	if runtime.GOOS == "windows" {
		name = "adder.exe"
	}

	finder := &AppBinaryFinder{DevLookup: true}

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	_, err := finder.Find()
	require.Error(t, err)

	path := filepath.Join(tmpDir, name)
	require.NoError(t, os.WriteFile(path, []byte("test"), 0o755))

	got, err := finder.Find()
	require.NoError(t, err)
	assert.Equal(t, path, got)
}

// TestAppBinaryFinderIgnoresCWDByDefault locks in the production gate: a
// finder with DevLookup disabled must not resolve a binary planted in the
// current working directory.
func TestAppBinaryFinderIgnoresCWDByDefault(t *testing.T) {
	name := "adder"
	if runtime.GOOS == "windows" {
		name = "adder.exe"
	}

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tmpDir, name), []byte("x"), 0o755),
	)

	_, err := (&AppBinaryFinder{}).Find()
	require.Error(t, err)
}

func TestValidateTrustedBinaryRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()

	// Relative path.
	require.Error(t, validateTrustedBinary("relative/adder"))
	// Missing file.
	require.Error(t, validateTrustedBinary(filepath.Join(dir, "missing")))
	// A directory is not a regular file.
	require.Error(t, validateTrustedBinary(dir))

	// Absolute regular file, not group/other-writable: accepted.
	good := filepath.Join(dir, "good")
	require.NoError(t, os.WriteFile(good, []byte("x"), 0o644))
	require.NoError(t, os.Chmod(good, 0o755))
	require.NoError(t, validateTrustedBinary(good))

	// Group/other-writable file: rejected. Windows os.Stat synthesises
	// permission bits, so the check is Unix-only.
	if runtime.GOOS != "windows" {
		bad := filepath.Join(dir, "bad")
		require.NoError(t, os.WriteFile(bad, []byte("x"), 0o644))
		require.NoError(t, os.Chmod(bad, 0o766))
		require.Error(t, validateTrustedBinary(bad))
	}
}

func TestServiceRenderingAndSafeStatusPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := ServiceConfig{
		BinaryPath: "/tmp/adder & friends",
		ConfigPath: "/tmp/config <one>.yaml",
		LogDir:     "/tmp/logs",
	}
	rendered, err := renderUnit(cfg)
	require.NoError(t, err)
	text := string(rendered)
	switch runtime.GOOS {
	case "darwin":
		assert.Contains(t, text, "adder &amp; friends")
		assert.Contains(t, text, "config &lt;one&gt;.yaml")
		assert.Contains(t, text, "/tmp/logs/adder.stdout.log")
	case "linux":
		assert.Contains(t, text, `ExecStart="/tmp/adder & friends"`)
		assert.Contains(t, text, `--config "/tmp/config <one>.yaml"`)
	case "freebsd":
		assert.Empty(t, text)
	}

	assert.NotEmpty(t, serviceUnitPath())

	status, err := ServiceStatusCheck()
	require.NoError(t, err)
	assert.Equal(t, ServiceNotRegistered, status)

	if runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" {
		err = (&OSManager{}).EnsureRunning()
		require.Error(t, err)
		assert.True(t,
			strings.Contains(err.Error(), "service not registered") ||
				strings.Contains(err.Error(), "not implemented"),
		)

		err = (&OSManager{}).RestartIfConfigChanged(
			"/tmp/adder",
			"/tmp/config.yaml",
		)
		require.Error(t, err)
		assert.True(t,
			strings.Contains(err.Error(), "service not registered") ||
				strings.Contains(err.Error(), "not implemented"),
		)
	}
}

func TestServiceConfigValidationAndStatusStrings(t *testing.T) {
	assert.Equal(t, "not registered", ServiceNotRegistered.String())
	assert.Equal(t, "registered", ServiceRegistered.String())
	assert.Equal(t, "running", ServiceRunning.String())
	assert.Equal(t, "unknown", ServiceStatus(99).String())

	assert.Error(t, ServiceConfig{}.Validate())
	assert.NoError(t, ServiceConfig{BinaryPath: "/tmp/adder"}.Validate())

	err := RegisterService(ServiceConfig{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "binary path"))
}

func TestRegisterServiceCreateDirError(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "freebsd" {
		t.Skip(
			"platform service command differs or is intentionally unsupported",
		)
	}

	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(notDir, []byte("x"), 0o600))
	t.Setenv("HOME", notDir)
	t.Setenv("XDG_CONFIG_HOME", notDir)

	err := RegisterService(ServiceConfig{BinaryPath: "/tmp/adder"})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "creating"))
}

func TestServiceLifecycleWithFakePlatformCommand(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "freebsd" {
		t.Skip(
			"platform service command differs or is intentionally unsupported",
		)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	installFakeServiceCommand(t)

	cfg := ServiceConfig{
		BinaryPath: "/tmp/adder",
		ConfigPath: "/tmp/config.yaml",
		LogDir:     filepath.Join(home, "logs"),
	}
	mgr := &OSManager{}

	require.NoError(t, mgr.EnsureRegistered(cfg.BinaryPath, cfg.ConfigPath))
	require.NoError(t, mgr.EnsureRegistered(cfg.BinaryPath, cfg.ConfigPath))

	status, err := mgr.Status()
	require.NoError(t, err)
	assert.Equal(t, ServiceRunning, status)

	require.NoError(t, mgr.EnsureRunning())

	require.NoError(
		t,
		os.WriteFile(serviceUnitPath(), []byte("stale"), 0o644),
	)
	require.NoError(
		t,
		mgr.RestartIfConfigChanged(cfg.BinaryPath, cfg.ConfigPath),
	)

	require.NoError(t, StartService())
	require.NoError(t, StopService())
	require.NoError(t, mgr.Stop())
	require.NoError(t, UnregisterService())
}

func installFakeServiceCommand(t *testing.T) {
	t.Helper()

	name := "systemctl"
	script := `#!/bin/sh
if [ "$1" = "--user" ] && [ "$2" = "is-active" ]; then
  echo active
fi
exit 0
`
	if runtime.GOOS == "darwin" {
		name = "launchctl"
		script = `#!/bin/sh
exit 0
`
	}

	binDir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755),
	)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
