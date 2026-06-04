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

//go:build darwin

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// syncFull issues fcntl(F_FULLFSYNC) on macOS so data is actually
// persisted to physical storage, not just handed to the drive cache.
// Apple's fsync(2) man page is explicit that fsync alone does NOT
// guarantee durability across power loss; F_FULLFSYNC is the
// documented mechanism. Falls back to plain f.Sync() on filesystems
// that don't support F_FULLFSYNC (e.g., some FUSE mounts return
// ENOTTY/EINVAL).
func syncFull(f *os.File) error {
	if _, err := unix.FcntlInt(
		f.Fd(),
		unix.F_FULLFSYNC,
		0,
	); err != nil {
		if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EINVAL) {
			return f.Sync()
		}
		return err
	}
	return nil
}
