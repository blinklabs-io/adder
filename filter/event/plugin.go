// Copyright 2023 Blink Labs, LLC.
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
	"strings"

	"github.com/blinklabs-io/snek/plugin"
)

var cmdlineOptions struct {
	eventType string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeFilter,
			Name:               "event",
			Description:        "filters events based on top-level event attributes",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "type",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies event type to filter on",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.eventType),
					CustomFlag:   "type",
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	pluginOptions := []EventOptionFunc{}
	if cmdlineOptions.eventType != "" {
		pluginOptions = append(
			pluginOptions,
			WithTypes(
				strings.Split(cmdlineOptions.eventType, ","),
			),
		)
	}
	p := New(pluginOptions...)
	return p
}
