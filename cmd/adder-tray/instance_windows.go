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

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// instanceMutexName is per-session (Local\) so one tray runs per logon session.
const instanceMutexName = `Local\io.blinklabs.adder.tray`

// acquireSingleInstance returns true if this is the only tray instance in the
// session. It creates a named mutex held for the process lifetime; a second
// instance sees ERROR_ALREADY_EXISTS. On unexpected errors it returns true
// (fail open) rather than blocking startup.
func acquireSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return true
	}
	// CreateMutex returns a valid handle even when the mutex already exists,
	// with err == ERROR_ALREADY_EXISTS. Intentionally leak the handle: it must
	// live for the whole process so the session-wide name stays claimed.
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		return false
	}
	return true
}
