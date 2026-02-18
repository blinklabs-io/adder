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

package tray

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceStatusString(t *testing.T) {
	tests := []struct {
		status ServiceStatus
		want   string
	}{
		{ServiceNotRegistered, "not registered"},
		{ServiceRegistered, "registered"},
		{ServiceRunning, "running"},
		{ServiceStatus(99), "unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.status.String())
	}
}

func TestServiceConfigValidate(t *testing.T) {
	t.Run("empty binary path", func(t *testing.T) {
		cfg := ServiceConfig{}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "binary path")
	})

	t.Run("valid config", func(t *testing.T) {
		cfg := ServiceConfig{BinaryPath: "/usr/bin/adder"}
		err := cfg.Validate()
		require.NoError(t, err)
	})
}

func TestRegisterServiceValidation(t *testing.T) {
	err := RegisterService(ServiceConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary path")
}
