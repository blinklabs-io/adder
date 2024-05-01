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

package push

import (
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var cmdlineOptions struct {
	serviceAccountFilePath string
	accessTokenUrl         string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "push",
			Description:        "Send push notifications for events",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{ // Define any options if needed
				{
					Name:         "serviceAccountFilePath",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the path to the service account file",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.serviceAccountFilePath),
				},
				{
					Name:         "accessTokenUrl",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the url to get access token from",
					DefaultValue: "https://www.googleapis.com/auth/firebase.messaging",
					Dest:         &(cmdlineOptions.accessTokenUrl),
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	p := New(
		WithLogger(
			logging.GetLogger().With("plugin", "output.push"),
		),
		WithAccessTokenUrl(cmdlineOptions.accessTokenUrl),
		WithServiceAccountFilePath(cmdlineOptions.serviceAccountFilePath),
	)
	return p
}
