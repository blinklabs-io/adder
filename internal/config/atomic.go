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

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SaveAtomic writes the data to disk using a temporary file and atomic rename.
func SaveAtomic(path string, data interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	tmpFile := path + ".tmp"
	f, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	// Safety net for the error paths below; cleared once we close explicitly so
	// the success path does not close the same file twice.
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	enc := yaml.NewEncoder(f)
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encoding configuration: %w", err)
	}

	err = f.Close()
	f = nil
	if err != nil {
		return fmt.Errorf("writing temporary file: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		// Attempt to cleanup on failure
		_ = os.Remove(tmpFile)
		return fmt.Errorf("renaming temporary file: %w", err)
	}

	return nil
}
