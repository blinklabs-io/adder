// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package restrictedfile

import (
	"errors"
	"os"
)

// Check verifies that an opened file grants no group or other permissions.
func Check(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("file permits group or other access")
	}
	return nil
}
