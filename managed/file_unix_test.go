// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package managed

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedConfigurationFIFORejectedWithoutWriter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "manifest.json"), 0o600))
	result := make(chan error, 1)
	go func() { _, err := readFile(dir, "manifest.json", 1024, false); result <- err }()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("a nonregular configuration file blocked the loader")
	}
}
