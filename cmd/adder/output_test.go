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

package main

import (
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestConfigureDefaultOutput(t *testing.T) {
	p := pipeline.New()
	c := config.New()
	require.Equal(t, "log", c.Output)
	resolved, err := c.ResolvePlugins(nil)
	require.NoError(t, err)
	options, err := resolved.Options(plugin.PluginTypeOutput, "log")
	require.NoError(t, err)
	require.Equal(t, "info", options.String("level"))
	require.NoError(t, configureOutput(p, c.Output, resolved))
	for range 2 {
		require.NoError(t, p.Start())
		require.True(t, p.IsRunning())
		require.NoError(t, p.Stop())
	}
}

func TestConfigureOutputRejectsUnknownName(t *testing.T) {
	for _, name := range []string{"typo-output", "none"} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t,
				configureOutput(pipeline.New(), name, &plugin.Configuration{}),
				"unknown output")
		})
	}
}
