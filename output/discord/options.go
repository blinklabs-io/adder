// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package discord

import (
	"net/http"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/gorilla/websocket"
)

// Option configures a Discord output.
type Option func(*DiscordOutput)

// WithCredentials sets the bot token and the authorized application, server, and channel.
func WithCredentials(token, applicationID, guildID, channelID string) Option {
	return func(d *DiscordOutput) {
		d.token, d.applicationID, d.guildID, d.channelID = token, applicationID, guildID, channelID
	}
}

// WithEventTypes sets the event types to publish.
func WithEventTypes(types map[string]bool) Option {
	return func(d *DiscordOutput) {
		d.selected = make(map[string]bool, len(types))
		for typ, enabled := range types {
			d.selected[typ] = enabled
		}
	}
}

// WithHTTPClient sets the HTTP transport, useful for local tests.
func WithHTTPClient(client *http.Client) Option { return func(d *DiscordOutput) { d.client = client } }

// WithGatewayDialer sets the Gateway transport, useful for local tests.
func WithGatewayDialer(dialer *websocket.Dialer) Option {
	return func(d *DiscordOutput) { d.dialer = dialer }
}

// WithAPIBaseURL sets the Discord REST endpoint, useful for local tests.
func WithAPIBaseURL(base string) Option { return func(d *DiscordOutput) { d.apiBaseURL = base } }

// WithLogger sets the plugin logger.
func WithLogger(logger plugin.Logger) Option { return func(d *DiscordOutput) { d.SetLogger(logger) } }
