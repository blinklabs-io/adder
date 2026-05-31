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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

// AppBinaryFinder locates a trusted `adder` binary to register and run as a
// system service. The resolved path is embedded into the OS service
// definition (systemd ExecStart, launchd ProgramArguments, schtasks /TR) and
// executed, so resolution must never trust an attacker-influenceable location.
type AppBinaryFinder struct {
	// DevLookup, when true, lets Find() fall back to the current working
	// directory (development/debug mode). It defaults to false and is never
	// set by production code, so shipped builds never resolve a binary from an
	// attacker-influenceable CWD.
	//
	// A build tag (//go:build dev) would compile the fallback out of release
	// binaries entirely — a stronger guarantee — but a struct field is used
	// here so the dev path stays exercisable under the standard `go test`
	// run. The guarantee therefore rests on no production caller ever setting
	// this true; keep it that way.
	DevLookup bool
}

func (f *AppBinaryFinder) Find() (string, error) {
	name := "adder"
	if runtime.GOOS == "windows" {
		name = "adder.exe"
	}

	// 1. Next to the running executable: the trusted install location.
	// Resolve symlinks first so we validate the real on-disk binary rather
	// than a link that may itself sit in a writable directory.
	if execPath, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(execPath); rerr == nil {
			execPath = resolved
		}
		candidate := filepath.Join(filepath.Dir(execPath), name)
		if err := validateTrustedBinary(candidate); err == nil {
			return candidate, nil
		} else {
			slog.Debug("adder binary next to executable is not usable",
				"path", candidate, "error", err)
		}
	}

	// 2. Current working directory: development/debug only. CWD is
	// attacker-influenceable and the result is executed as a persistent
	// service, so this is never consulted in production builds.
	if f.DevLookup {
		if cwd, err := os.Getwd(); err == nil {
			candidate := filepath.Join(cwd, name)
			if err := validateTrustedBinary(candidate); err == nil {
				slog.Warn("using adder binary from working directory (dev mode)",
					"path", candidate)
				return candidate, nil
			} else {
				slog.Debug("adder binary in working directory is not usable",
					"path", candidate, "error", err)
			}
		}
	}

	return "", fmt.Errorf("could not find a trusted %s next to the executable", name)
}

// validateTrustedBinary verifies that path is safe to register as a service
// executable: an absolute path to an existing regular file that is not
// writable by group or other. A service binary that untrusted users can
// overwrite is a privilege-escalation vector, so such a path is rejected.
func validateTrustedBinary(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("path is not absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	// Unix permission bits are not meaningful on Windows (os.Stat synthesises
	// them from the read-only attribute), so only enforce the writable-by-
	// others check on Unix-like systems.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf(
			"binary is writable by group/other (mode %#o)",
			info.Mode().Perm(),
		)
	}
	return nil
}
