// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package restrictedfile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCheckWindowsACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.token")
	if err := os.WriteFile(path, []byte("test token"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner := user.User.Sid.String()
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	fileOwner, _, err := descriptor.Owner()
	if err != nil || fileOwner == nil {
		t.Fatal("file owner unavailable", err)
	}
	t.Logf("file owner: process user=%v administrators=%v system=%v",
		fileOwner.Equals(user.User.Sid),
		fileOwner.IsWellKnown(windows.WinBuiltinAdministratorsSid),
		fileOwner.IsWellKnown(windows.WinLocalSystemSid))
	setACL := func(sddl string) {
		t.Helper()
		descriptor, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil, nil, dacl, nil); err != nil {
			t.Fatal(err)
		}
	}
	protected := "D:P(A;;GA;;;" + owner + ")(A;;GA;;;SY)(A;;GA;;;BA)"
	setACL(protected)
	if err := Check(file); err != nil {
		t.Fatalf("restricted process-account ACL rejected: %v", err)
	}
	setACL(protected + "(A;;GR;;;WD)")
	if err := Check(file); err == nil {
		t.Fatal("Everyone read grant accepted")
	}
}
