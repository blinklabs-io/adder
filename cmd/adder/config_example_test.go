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
	"os"
	"regexp"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

func TestExampleConfigUsesRegisteredPluginOptions(t *testing.T) {
	data, err := os.ReadFile("../../config.yaml.example")
	require.NoError(t, err)
	registered := make(map[string]map[string]map[string]bool)
	for _, role := range []plugin.PluginType{
		plugin.PluginTypeInput,
		plugin.PluginTypeFilter,
		plugin.PluginTypeOutput,
	} {
		entries := make(map[string]map[string]bool)
		for _, entry := range plugin.GetPlugins(role) {
			options := make(map[string]bool)
			for _, option := range entry.Options {
				options[option.Name] = true
			}
			entries[entry.Name] = options
		}
		registered[plugin.PluginTypeName(role)] = entries
	}
	// Uncomment optional YAML keys as well, so dormant example typos cannot
	// silently become unsupported options when a user enables them.
	optional := regexp.MustCompile(`(?m)^(\s*)#([a-z][a-z_-]*:)`)
	for name, document := range map[string][]byte{
		"defaults":          data,
		"optional settings": optional.ReplaceAll(data, []byte("${1}${2}")),
	} {
		t.Run(name, func(t *testing.T) {
			var cfg config.Config
			require.NoError(t, yaml.UnmarshalStrict(document, &cfg))
			for role, entries := range cfg.Plugin {
				require.Contains(t, registered, role)
				for name, options := range entries {
					require.Contains(t, registered[role], name)
					for key := range options {
						require.Contains(
							t,
							registered[role][name],
							key,
							"plugins.%s.%s.%v is silently ignored",
							role,
							name,
							key,
						)
					}
				}
			}
		})
	}
}
