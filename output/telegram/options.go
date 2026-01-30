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
	"time"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/go-telegram/bot/models"
)

// TelegramOptionFunc is a function type for configuring TelegramOutput
type TelegramOptionFunc func(*TelegramOutput)

// WithLogger specifies the logger object to use for logging messages
func WithLogger(logger plugin.Logger) TelegramOptionFunc {
	return func(t *TelegramOutput) {
		t.logger = logger
	}
}

// WithBotToken specifies the Telegram Bot API token
// This token is obtained from @BotFather on Telegram
func WithBotToken(token string) TelegramOptionFunc {
	return func(t *TelegramOutput) {
		t.botToken = token
	}
}

// WithChatID specifies the chat ID to send messages to
// This can be a user ID, group ID, or channel ID
// For groups/channels, the ID is typically negative
func WithChatID(chatID int64) TelegramOptionFunc {
	return func(t *TelegramOutput) {
		t.chatID = chatID
	}
}

// WithParseMode specifies the message parse mode
// Options: HTML, Markdown (legacy), MarkdownV2 (default markdown)
func WithParseMode(mode string) TelegramOptionFunc {
	return func(t *TelegramOutput) {
		switch mode {
		case "HTML":
			t.parseMode = models.ParseModeHTML
		case "Markdown":
			t.parseMode = models.ParseModeMarkdownV1
		case "MarkdownV2":
			t.parseMode = models.ParseModeMarkdown
		default:
			t.parseMode = models.ParseModeHTML
		}
	}
}

// WithDisableLinkPreview disables link preview in messages
func WithDisableLinkPreview(disable bool) TelegramOptionFunc {
	return func(t *TelegramOutput) {
		t.disablePreview = disable
	}
}

// WithRetryConfig specifies the retry configuration for message delivery
func WithRetryConfig(
	maxRetries int,
	initialBackoff, maxBackoff time.Duration,
) TelegramOptionFunc {
	return func(t *TelegramOutput) {
		if maxRetries >= 0 {
			t.maxRetries = maxRetries
		}
		if initialBackoff > 0 {
			t.initialBackoff = initialBackoff
		}
		if maxBackoff > 0 {
			t.maxBackoff = maxBackoff
		}
	}
}
