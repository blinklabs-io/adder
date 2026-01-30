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
	"strconv"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var cmdlineOptions struct {
	botToken       string
	chatID         string // String to support int64 values (groups have negative IDs)
	parseMode      string
	disablePreview bool
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "telegram",
			Description:        "send events to a Telegram chat or channel",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "bot-token",
					Type:         plugin.PluginOptionTypeString,
					Description:  "Telegram Bot API token (from @BotFather)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.botToken),
				},
				{
					Name:         "chat-id",
					Type:         plugin.PluginOptionTypeString,
					Description:  "Telegram chat ID to send messages to (user, group, or channel)",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.chatID),
				},
				{
					Name:         "parse-mode",
					Type:         plugin.PluginOptionTypeString,
					Description:  "message parse mode (HTML, Markdown, MarkdownV2)",
					DefaultValue: "HTML",
					Dest:         &(cmdlineOptions.parseMode),
				},
				{
					Name:         "disable-preview",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "disable link preview in messages",
					DefaultValue: false,
					Dest:         &(cmdlineOptions.disablePreview),
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	logger := logging.GetLogger()

	if cmdlineOptions.chatID == "" {
		logger.Error("chat ID is required for Telegram output (--output-telegram-chat-id or OUTPUT_TELEGRAM_CHAT_ID)")
		return nil
	}

	// Parse chat ID from string to int64
	chatID, err := strconv.ParseInt(cmdlineOptions.chatID, 10, 64)
	if err != nil {
		logger.Error("invalid chat ID", "error", err, "chat_id", cmdlineOptions.chatID)
		return nil
	}

	p, err := New(
		WithLogger(
			logger.With("plugin", "output.telegram"),
		),
		WithBotToken(cmdlineOptions.botToken),
		WithChatID(chatID),
		WithParseMode(cmdlineOptions.parseMode),
		WithDisableLinkPreview(cmdlineOptions.disablePreview),
	)
	if err != nil {
		logger.Error("failed to create Telegram output", "error", err)
		return nil
	}
	return p
}
