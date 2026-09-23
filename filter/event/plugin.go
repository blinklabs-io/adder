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

package event

import (
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeFilter,
			Name:               "event",
			Description:        "filters events based on top-level event attributes",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "type",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies event type to filter on",
					DefaultValue: "",
					CustomFlag:   "type",
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	pluginOptions := []EventOptionFunc{
		WithLogger(
			logging.GetLogger().With("plugin", "filter.event"),
		),
	}
	if eventTypes := plugin.SplitAndTrim(
		values.String("type"),
	); len(eventTypes) > 0 {
		pluginOptions = append(
			pluginOptions,
			WithTypes(eventTypes),
		)
	}
	p := New(pluginOptions...)
	return p, nil
}
