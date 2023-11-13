// Copyright 2023 Blink Labs Software
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

package notify

import (
	"github.com/blinklabs-io/snek/internal/logging"
	"github.com/blinklabs-io/snek/plugin"
)

var cmdlineOptions struct {
	title string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "notify",
			Description:        "display events using operating system notifications",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "title",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the title to use",
					DefaultValue: "Snek",
					Dest:         &(cmdlineOptions.title),
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	p := New(
		WithLogger(
			logging.GetLogger().With("plugin", "output.notify"),
		),
		WithTitle(cmdlineOptions.title),
	)
	return p
}
