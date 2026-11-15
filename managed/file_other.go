// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package managed

import "os"

func openManagedFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	return root.OpenFile(name, flags, mode)
}
