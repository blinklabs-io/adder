// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package push

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestFactoryConstructionResult(t *testing.T) {
	credentials := filepath.Join(t.TempDir(), "service-account.json")
	require.NoError(t, os.WriteFile(credentials, []byte(`{"project_id":"test"}`), 0o600))
	for _, path := range []string{"", credentials} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg, err := plugin.ResolveConfig(
				map[string]map[string]map[string]any{
					"output": {"push": {"serviceAccountFilePath": path}},
				}, nil, func(string) (string, bool) { return "", false },
			)
			require.NoError(t, err)
			options, err := cfg.Options(plugin.PluginTypeOutput, "push")
			require.NoError(t, err)
			p, err := newFromOptions(options)
			if path == "" {
				require.ErrorContains(t, err, "service account file path is required")
				require.True(t, p == nil, "failed factory returned a non-nil interface")
			} else {
				require.NoError(t, err)
				require.IsType(t, &PushOutput{}, p)
			}
		})
	}
}
