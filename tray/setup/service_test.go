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

package setup

import (
	"path/filepath"
	"testing"
)

// spyRegistrar records whether EnsureRegistered took the repair action
// (registerService) so the test can assert the skip/repair decision itself,
// not merely the existingUnit predicate that feeds it. Runs on any host OS.
type spyRegistrar struct {
	unit          []byte
	registerCalls int
}

func (s *spyRegistrar) existingUnit() []byte { return s.unit }

func (s *spyRegistrar) registerService(ServiceConfig) error {
	s.registerCalls++
	return nil
}

// TestEnsureRegisteredAction verifies EnsureRegistered skips vs repairs based on
// the current registration state, asserting the observable action
// (registerService call count) rather than the existingUnit return value.
func TestEnsureRegisteredAction(t *testing.T) {
	tmp := t.TempDir()
	// Mirror the cfg EnsureRegistered builds internally (it uses LogDir(), not a
	// caller-supplied LogDir), so the skip-case "existing" equals the desired
	// unit EnsureRegistered computes.
	cfg := ServiceConfig{
		BinaryPath: filepath.Join(tmp, "adder"),
		ConfigPath: filepath.Join(tmp, "config.yaml"),
		LogDir:     LogDir(),
	}
	desired, err := renderUnit(cfg)
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}

	tests := []struct {
		name          string
		existing      []byte
		wantRegisters int
	}{
		{
			name:          "correct registration is skipped",
			existing:      desired,
			wantRegisters: 0,
		},
		{
			name:          "missing registration is repaired",
			existing:      nil,
			wantRegisters: 1,
		},
		{
			name:          "stale registration is repaired",
			existing:      []byte("stale-unit"),
			wantRegisters: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &spyRegistrar{unit: tt.existing}
			mgr := &OSManager{reg: spy}
			if err := mgr.EnsureRegistered(
				cfg.BinaryPath, cfg.ConfigPath,
			); err != nil {
				t.Fatalf("EnsureRegistered: %v", err)
			}
			if spy.registerCalls != tt.wantRegisters {
				t.Fatalf(
					"registerService calls = %d, want %d",
					spy.registerCalls, tt.wantRegisters,
				)
			}
		})
	}
}
