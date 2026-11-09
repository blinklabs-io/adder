// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package matrix

import (
	"errors"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "matrix",
		Description:        "send selected events to Matrix rooms",
		NewFromOptionsFunc: newFromOptions,
		Options: []plugin.PluginOption{
			{Name: "homeserver-url", Type: plugin.PluginOptionTypeString, Description: "Matrix homeserver base URL", DefaultValue: ""},
			{Name: "access-token", Type: plugin.PluginOptionTypeString, Description: "Matrix access token", DefaultValue: ""},
			{Name: "room-ids", Type: plugin.PluginOptionTypeString, Description: "comma-separated Matrix room IDs", DefaultValue: ""},
			{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
		},
	})
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	homeserver := values.String("homeserver-url")
	if err := validateHomeserver(homeserver); err != nil {
		return nil, err
	}
	token := values.String("access-token")
	if token == "" {
		return nil, errors.New("access-token is required")
	}
	rooms := plugin.SplitAndTrim(values.String("room-ids"))
	if len(rooms) == 0 {
		return nil, errors.New("room-ids must contain at least one room")
	}
	for _, room := range rooms {
		if len(room) < 4 || room[0] != '!' {
			return nil, errors.New("room-ids must contain Matrix room IDs")
		}
	}
	types, err := selectedTypes(plugin.SplitAndTrim(values.String("event-types")))
	if err != nil {
		return nil, err
	}
	return New(WithHomeserver(homeserver), WithAccessToken(token), WithRooms(rooms...), WithEventTypes(types), WithLogger(logging.GetLogger().With("plugin", "output.matrix"))), nil
}
