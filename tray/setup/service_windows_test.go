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

func TestSameExecutablePath(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		want     bool
	}{
		{
			name:     "exact path",
			expected: `C:\Program Files\Adder\adder.exe`,
			actual:   `C:\Program Files\Adder\adder.exe`,
			want:     true,
		},
		{
			name:     "case insensitive",
			expected: `C:\Program Files\Adder\adder.exe`,
			actual:   `c:\PROGRAM FILES\ADDER\ADDER.EXE`,
			want:     true,
		},
		{
			name:     "cleaned equivalent path",
			expected: `C:\Program Files\Adder\bin\..\adder.exe`,
			actual:   `C:\Program Files\Adder\adder.exe`,
			want:     true,
		},
		{
			name:     "same image in another directory",
			expected: `C:\Program Files\Adder\adder.exe`,
			actual:   `C:\Other\adder.exe`,
			want:     false,
		},
		{
			name:     "missing expected path",
			expected: "",
			actual:   `C:\Program Files\Adder\adder.exe`,
			want:     false,
		},
		{
			name:     "missing actual path",
			expected: `C:\Program Files\Adder\adder.exe`,
			actual:   "",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameExecutablePath(tt.expected, tt.actual); got != tt.want {
				t.Fatalf(
					"sameExecutablePath(%q, %q) = %v, want %v",
					tt.expected, tt.actual, got, tt.want,
				)
			}
		})
	}
}

func TestServiceStatusValidatesTrackedProcessPath(t *testing.T) {
	tempPath := `Software\BlinkLabs\TestAdderStatus_` + t.Name()
	_ = registry.DeleteKey(registry.CURRENT_USER, tempPath)
	defer func() { _ = registry.DeleteKey(registry.CURRENT_USER, tempPath) }()

	oldHive := runKeyRegistryHive
	oldPath := runKeyRegistryPath
	runKeyRegistryHive = registry.CURRENT_USER
	runKeyRegistryPath = tempPath
	defer func() {
		runKeyRegistryHive = oldHive
		runKeyRegistryPath = oldPath
	}()

	tmpDir := t.TempDir()
	t.Setenv("ADDER_TRAY_CONFIG_DIR", filepath.Join(tmpDir, "config"))
	t.Setenv("ADDER_TRAY_LOG_DIR", filepath.Join(tmpDir, "logs"))

	pid := uint32(os.Getpid())
	actualPath, err := fullProcessPath(pid)
	if err != nil {
		t.Fatalf("querying current process path: %v", err)
	}
	if !processAlive(pid) {
		t.Fatal("current test process should be alive")
	}
	if err := writeEnginePID(int(pid)); err != nil {
		t.Fatalf("writing live unregistered pid: %v", err)
	}
	status, err := serviceStatusCheck()
	if err != nil {
		t.Fatalf("checking unregistered pid status: %v", err)
	}
	if status != ServiceNotRegistered {
		t.Fatalf("unregistered pid status = %s, want not registered", status)
	}
	if _, err := os.Stat(enginePIDFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unverified pid file was not removed: %v", err)
	}

	foreignPath := filepath.Join(
		tmpDir, "other-install", filepath.Base(actualPath),
	)
	if filepath.Base(foreignPath) != filepath.Base(actualPath) {
		t.Fatal("test setup must use the same executable image name")
	}
	if sameExecutablePath(foreignPath, actualPath) {
		t.Fatal("test setup must use different full executable paths")
	}

	if err := registerService(ServiceConfig{BinaryPath: foreignPath}); err != nil {
		t.Fatalf("registering foreign-path engine command: %v", err)
	}
	if err := writeEnginePID(int(pid)); err != nil {
		t.Fatalf("writing live foreign pid: %v", err)
	}

	status, err = serviceStatusCheck()
	if err != nil {
		t.Fatalf("checking reused pid status: %v", err)
	}
	if status != ServiceRegistered {
		t.Fatalf("reused pid status = %s, want registered", status)
	}
	if _, err := os.Stat(enginePIDFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale pid file was not removed: %v", err)
	}
	if !processAlive(pid) {
		t.Fatal("status check terminated the unrelated test process")
	}

	if err := registerService(ServiceConfig{BinaryPath: actualPath}); err != nil {
		t.Fatalf("registering matching engine command: %v", err)
	}
	if err := writeEnginePID(int(pid)); err != nil {
		t.Fatalf("writing matching engine pid: %v", err)
	}

	status, err = serviceStatusCheck()
	if err != nil {
		t.Fatalf("checking matching pid status: %v", err)
	}
	if status != ServiceRunning {
		t.Fatalf("matching pid status = %s, want running", status)
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

func TestRemoveEnginePIDIfPreservesReplacement(t *testing.T) {
	t.Setenv("ADDER_TRAY_CONFIG_DIR", t.TempDir())
	if err := writeEnginePID(84); err != nil {
		t.Fatal(err)
	}

	removeEnginePIDIf(42)

	pid, ok := readEnginePID()
	if !ok || pid != 84 {
		t.Fatalf("replacement pid = %d, %v; want 84, true", pid, ok)
	}
	removeEnginePIDIf(84)
	if _, err := os.Stat(enginePIDFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("matching stale pid file was not removed: %v", err)
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
