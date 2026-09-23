// Copyright 2025 Blink Labs Software
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

package telegram

import (
	"errors"
	"strconv"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "telegram",
			Description:        "send events to a Telegram chat or channel",
			NewFromOptionsFunc: newFromOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "bot-token",
					Type:         plugin.PluginOptionTypeString,
					Description:  "Telegram Bot API token (from @BotFather)",
					DefaultValue: "",
				},
				{
					Name:         "chat-id",
					Type:         plugin.PluginOptionTypeString,
					Description:  "Telegram chat ID to send messages to (user, group, or channel)",
					DefaultValue: "",
				},
				{
					Name:         "parse-mode",
					Type:         plugin.PluginOptionTypeString,
					Description:  "message parse mode (HTML, Markdown, MarkdownV2)",
					DefaultValue: "HTML",
				},
				{
					Name:         "disable-preview",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "disable link preview in messages",
					DefaultValue: false,
				},
			},
		},
	)
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	switch values.String("parse-mode") {
	case "", "HTML", "Markdown", "MarkdownV2":
	default:
		return nil, errors.New(
			"parse-mode must be empty, HTML, Markdown, or MarkdownV2",
		)
	}

	logger := logging.GetLogger()

	if values.String("chat-id") == "" {
		return nil, errors.New("chat-id is required")
	}

	// Parse chat ID from string to int64
	chatID, err := strconv.ParseInt(values.String("chat-id"), 10, 64)
	if err != nil || chatID == 0 {
		return nil, errors.New("chat-id must be a signed 64-bit integer")
	}

	p, err := New(
		WithLogger(
			logger.With("plugin", "output.telegram"),
		),
		WithBotToken(values.String("bot-token")),
		WithChatID(chatID),
		WithParseMode(values.String("parse-mode")),
		WithDisableLinkPreview(values.Bool("disable-preview")),
	)
	return p, err
}
