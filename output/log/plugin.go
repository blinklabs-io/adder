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

package log

import (
	"errors"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "log",
			Description:        "display events to the console or write to a file",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "level",
					Type:         plugin.PluginOptionTypeString,
					Description:  "logging threshold: debug/info emit events; warn/error suppress events; also filters diagnostics",
					DefaultValue: "info",
				},
				{
					Name:         "format",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the output format: text (human-readable, default) or json (machine-parseable)",
					DefaultValue: "text",
				},
				{
					Name:         "path",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the file path to write logs to (default is stdout)",
					DefaultValue: "",
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	level, err := logging.ParseLevel(values.String("level"))
	if err != nil {
		return nil, err
	}
	if values.String("format") != "text" && values.String("format") != "json" {
		return nil, errors.New("format must be text or json")
	}

	p := New(
		WithLevel(level),
		WithLogger(
			logging.GetLogger().With("plugin", "output.log"),
		),
		WithFormat(values.String("format")),
		WithFilePath(values.String("path")),
	)
	return p, nil
}
