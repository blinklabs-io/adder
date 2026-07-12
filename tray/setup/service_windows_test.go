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

//go:build windows

package setup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestIsOurEngine covers the decision that gates whether an
// unopenable-but-alive PID blocks startup. A stale PID reused by a
// protected/foreign process (image unknown or mismatched) must NOT be treated
// as our engine, so startService can recover instead of deadlocking on a
// "cannot terminate" error.
func TestIsOurEngine(t *testing.T) {
	tests := []struct {
		name        string
		expectImage string
		actual      string
		known       bool
		want        bool
	}{
		{
			name:        "match is our engine",
			expectImage: "adder.exe",
			actual:      "adder.exe",
			known:       true,
			want:        true,
		},
		{
			name:        "case-insensitive match",
			expectImage: "adder.exe",
			actual:      "ADDER.EXE",
			known:       true,
			want:        true,
		},
		{
			name:        "stale pid reused by foreign process",
			expectImage: "adder.exe",
			actual:      "svchost.exe",
			known:       true,
			want:        false,
		},
		{
			name:        "image not found in snapshot",
			expectImage: "adder.exe",
			actual:      "",
			known:       false,
			want:        false,
		},
		{
			name:        "expected image unknown",
			expectImage: "",
			actual:      "adder.exe",
			known:       true,
			want:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOurEngine(tt.expectImage, tt.actual, tt.known)
			if got != tt.want {
				t.Fatalf(
					"isOurEngine(%q, %q, %v) = %v, want %v",
					tt.expectImage, tt.actual, tt.known, got, tt.want,
				)
			}
		})
	}
}

func TestWriteEnginePIDReturnsError(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADDER_TRAY_CONFIG_DIR", blocked)

	if err := writeEnginePID(42); err == nil {
		t.Fatal("expected PID write failure")
	}
}

func TestStopTrackedEngineRetainsPIDOnFailure(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ADDER_TRAY_CONFIG_DIR", configDir)
	if err := writeEnginePID(42); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("termination failed")
	err := stopTrackedEngine(42, func(uint32, string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(enginePIDFile()); err != nil {
		t.Fatalf("PID file was removed after failed termination: %v", err)
	}
}

func TestIsUntrackedEnginePath(t *testing.T) {
	installDir := `C:\Program Files\Adder`
	if !isUntrackedEnginePath(
		`C:\Program Files\Adder\adder.exe`, installDir,
	) {
		t.Fatal("engine under install directory should be recovered")
	}
	if isUntrackedEnginePath(
		`C:\Program Files\Adder\adder-tray.exe`, installDir,
	) {
		t.Fatal("tray process must not be terminated")
	}
	if isUntrackedEnginePath(`C:\Other\adder.exe`, installDir) {
		t.Fatal("unrelated engine path must not be terminated")
	}
}

// TestWindowsRegistryRegistrationState verifies that existingUnit on Windows
// integrates both the tray's HKCU Run autostart key and the engine command
// mirror file. If either is missing or stale, it triggers a repair.
func TestWindowsRegistryRegistrationState(t *testing.T) {
	// Redirect registry access to a temporary HKCU path so the test does not
	// modify the user's real Run key.
	tempPath := `Software\BlinkLabs\TestAdderRegistry_` + t.Name()

	// Ensure we start with a clean slate
	_ = registry.DeleteKey(registry.CURRENT_USER, tempPath)
	defer func() { _ = registry.DeleteKey(registry.CURRENT_USER, tempPath) }()

	// Back up and restore registry package variables
	oldHive := runKeyRegistryHive
	oldPath := runKeyRegistryPath
	runKeyRegistryHive = registry.CURRENT_USER
	runKeyRegistryPath = tempPath
	defer func() {
		runKeyRegistryHive = oldHive
		runKeyRegistryPath = oldPath
	}()

	// Create the temporary key so Opens will succeed
	k, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		tempPath,
		registry.SET_VALUE,
	)
	if err != nil {
		t.Fatalf("failed to create temporary registry key: %v", err)
	}
	k.Close()

	// Redirect home directory and other paths to temp dir for test isolation
	tmpDir := t.TempDir()
	t.Setenv("ADDER_TRAY_LOG_DIR", filepath.Join(tmpDir, "logs"))
	t.Setenv("ADDER_TRAY_CONFIG_DIR", filepath.Join(tmpDir, "config"))

	// Initialize setup variables
	cfg := ServiceConfig{
		BinaryPath: filepath.Join(tmpDir, "adder.exe"),
		ConfigPath: filepath.Join(tmpDir, "config.yaml"),
		LogDir:     filepath.Join(tmpDir, "logs"),
	}

	desiredUnit, err := renderUnit(cfg)
	if err != nil {
		t.Fatalf("renderUnit failed: %v", err)
	}

	// 1. Run value missing -> existingUnit() returns nil (repair registration)
	t.Run("missing run value", func(t *testing.T) {
		got := existingUnit()
		if got != nil {
			t.Fatalf(
				"expected nil existingUnit when Run key is missing, got %s",
				got,
			)
		}
	})

	// 2. Perform a registerService
	if err := registerService(cfg); err != nil {
		t.Fatalf("registerService failed: %v", err)
	}

	// 3. Run value correct -> existingUnit() returns mirror command bytes
	t.Run("correct run value", func(t *testing.T) {
		got := existingUnit()
		if got == nil {
			t.Fatal("expected non-nil existingUnit when Run key is correct")
		}
		if !bytes.Equal(got, desiredUnit) {
			t.Fatalf("expected existingUnit %s, got %s", desiredUnit, got)
		}
	})

	// 4. Run value points elsewhere -> existingUnit() returns nil (repair)
	t.Run("stale run value pointing elsewhere", func(t *testing.T) {
		k, err := registry.OpenKey(
			registry.CURRENT_USER,
			tempPath,
			registry.SET_VALUE,
		)
		if err != nil {
			t.Fatalf("failed to open temp key: %v", err)
		}
		defer k.Close()
		err = k.SetStringValue(runValueName, `C:\Windows\System32\cmd.exe`)
		if err != nil {
			t.Fatalf("failed to set fake Run value: %v", err)
		}

		got := existingUnit()
		if got != nil {
			t.Fatalf(
				"expected nil existingUnit when Run key points elsewhere, got %s",
				got,
			)
		}
	})
}
