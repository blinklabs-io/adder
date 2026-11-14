// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package restrictedfile

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errUnrestricted = errors.New("file ACL grants access outside its owner or trusted administrators")

// Check verifies the owner and DACL of the already opened file handle.
func Check(f *os.File) error {
	if f == nil {
		return errUnrestricted
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil {
		return errUnrestricted
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return errUnrestricted
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return errUnrestricted
	}
	if !owner.Equals(user.User.Sid) &&
		!owner.IsWellKnown(windows.WinLocalSystemSid) &&
		!owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return errUnrestricted
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errUnrestricted
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil {
			return errUnrestricted
		}
		if ace.Header.AceSize < 4 {
			return errUnrestricted
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if ace.Header.AceSize < 16 {
				return errUnrestricted
			}
			trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if int(ace.Header.AceSize) < 16+4*int(trustee.SubAuthorityCount()) {
				return errUnrestricted
			}
			if !trustee.IsValid() ||
				(!trustee.Equals(owner) &&
					!trustee.IsWellKnown(windows.WinLocalSystemSid) &&
					!trustee.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
				return errUnrestricted
			}
		default:
			return errUnrestricted
		}
	}
	return nil
}
