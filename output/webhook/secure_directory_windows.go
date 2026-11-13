// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package webhook

import "path/filepath"

// Windows directory access is governed by ACLs. Operators must grant write
// access to the Adder account only; Unix mode checks are not meaningful here.
func validateStateDirectory(path string) (string, error) { return filepath.Abs(path) }
