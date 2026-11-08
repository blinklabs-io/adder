// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package discord

import (
	"errors"
	"strings"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var eventTypes = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
}

func init() {
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "discord",
		Description:        "post events and serve read-only commands in a Discord channel",
		NewFromOptionsFunc: newFromOptions,
		Options: []plugin.PluginOption{
			{Name: "bot-token", Type: plugin.PluginOptionTypeString, Description: "Discord bot token", DefaultValue: ""},
			{Name: "application-id", Type: plugin.PluginOptionTypeString, Description: "Discord application ID", DefaultValue: ""},
			{Name: "guild-id", Type: plugin.PluginOptionTypeString, Description: "allowed Discord server ID", DefaultValue: ""},
			{Name: "channel-id", Type: plugin.PluginOptionTypeString, Description: "allowed Discord channel ID", DefaultValue: ""},
			{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
		},
	})
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(values.String("event-types")) {
		typ, ok := eventTypes[strings.ToLower(name)]
		if !ok {
			return nil, errors.New("event-types contains an unsupported type")
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must not be empty")
	}
	return New(WithCredentials(values.String("bot-token"), values.String("application-id"), values.String("guild-id"), values.String("channel-id")), WithEventTypes(selected), WithLogger(logging.GetLogger().With("plugin", "output.discord")))
}
