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

package notifyjson

import "github.com/blinklabs-io/adder/plugin"

var cmdlineOptions struct {
	config string
}

func init() {
	plugin.Register(plugin.PluginEntry{
		Type:               plugin.PluginTypeOutput,
		Name:               "notify-json",
		Description:        "emit target-aware desktop notification requests as NDJSON",
		NewFromOptionsFunc: NewFromCmdlineOptions,
		Options: []plugin.PluginOption{
			{
				Name:         "config",
				Type:         plugin.PluginOptionTypeString,
				Description:  "path to a versioned notification JSON configuration",
				DefaultValue: "",
				Dest:         &cmdlineOptions.config,
			},
		},
	})
}

func NewFromCmdlineOptions() plugin.Plugin {
	return New(WithConfigPath(cmdlineOptions.config))
}
