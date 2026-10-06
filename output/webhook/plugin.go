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
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "webhook",
			Description:        "send events via HTTP POST to a webhook server",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "format",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the webhook payload format to use",
					DefaultValue: "adder",
				},
				{
					Name:         "url",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the url to use",
					DefaultValue: "http://localhost:3000",
				},
				{
					Name:         "tls-skip-verify",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "skip tls verification (for self-signed certs)",
					DefaultValue: false,
				},
				{
					Name:         "username",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the username for basic auth",
					DefaultValue: "",
				},
				{
					Name:         "password",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the password for basic auth",
					DefaultValue: "",
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	if values.String("format") != "adder" &&
		values.String("format") != "discord" {
		return nil, errors.New("format must be adder or discord")
	}
	if err := plugin.ValidateHTTPURL(values.String("url")); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}

	p := New(
		WithLogger(
			logging.GetLogger().With("plugin", "output.webhook"),
		),
		WithUrl(values.String("url"), values.Bool("tls-skip-verify")),
		WithBasicAuth(values.String("username"), values.String("password")),
		WithFormat(values.String("format")),
	)
	return p, nil
}
