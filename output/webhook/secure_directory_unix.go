// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package webhook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// validateStateDirectory returns a canonical directory path that SQLite must
// use, excluding mutable symlink aliases from its later pathname resolution.
// Unix mode checks do not inspect ACLs; operators must keep ACL write access
// private as well.
func validateStateDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !parent.IsDir() {
		return "", errors.New("state-path parent must be a directory, not a symlink")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if err := validateDirectoryChain(real); err != nil {
		return "", err
	}
	return real, nil
}

func validateDirectoryChain(path string) error {
	current := string(os.PathSeparator)
	parts := strings.Split(strings.TrimPrefix(path, current), current)
	for idx, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect %q: %w", current, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("inspect %q: missing Unix owner", current)
		}
		if stat.Uid != 0 && int64(stat.Uid) != int64(os.Geteuid()) {
			return fmt.Errorf("state-path directory component %q is owned by another user", current)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("state-path directory component %q changed during validation", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("state-path component %q is not a directory", current)
		}
		if info.Mode().Perm()&0o022 != 0 &&
			(idx == len(parts)-1 || stat.Uid != 0 || info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("state-path directory component %q is writable by another user", current)
		}
	}
	return nil
}
