// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package managed

import (
	"os"
	"syscall"
)

// Nonblocking open lets the regular-file check reject a FIFO without waiting
// for a peer; it also covers replacement between metadata lookup and open.
func openManagedFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	return root.OpenFile(name, flags|syscall.O_NONBLOCK, mode)
}
