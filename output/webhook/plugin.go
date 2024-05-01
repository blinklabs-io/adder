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

package webhook

import (
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var cmdlineOptions struct {
	format   string
	url      string
	username string
	password string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "webhook",
			Description:        "send events via HTTP POST to a webhook server",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "format",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the webhook payload format to use",
					DefaultValue: "adder",
					Dest:         &(cmdlineOptions.format),
				},
				{
					Name:         "url",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the url to use",
					DefaultValue: "http://localhost:3000",
					Dest:         &(cmdlineOptions.url),
				},
				{
					Name:         "username",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the username for basic auth",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.username),
				},
				{
					Name:         "password",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the password for basic auth",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.password),
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	p := New(
		WithLogger(
			logging.GetLogger().With("plugin", "output.webhook"),
		),
		WithUrl(cmdlineOptions.url),
		WithBasicAuth(cmdlineOptions.username, cmdlineOptions.password),
		WithFormat(cmdlineOptions.format),
	)
	return p
}
