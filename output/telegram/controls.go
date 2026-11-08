// Copyright 2026 Blink Labs Software
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"gopkg.in/yaml.v3"
)

type notificationSettings struct {
	ChatID       int64 `yaml:"chat_id"`
	Blocks       bool  `yaml:"blocks"`
	Transactions bool  `yaml:"transactions"`
	Rollbacks    bool  `yaml:"rollbacks"`
	Governance   bool  `yaml:"governance"`
}

func defaultNotificationSettings() notificationSettings {
	return notificationSettings{
		Blocks: true, Transactions: true, Rollbacks: true, Governance: true,
	}
}

func (t *TelegramOutput) loadSettings() error {
	if t.settingsFile == "" {
		return nil
	}
	data, err := os.ReadFile(t.settingsFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Telegram settings: %w", err)
	}
	settings := defaultNotificationSettings()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&settings); err != nil {
		return fmt.Errorf("decoding Telegram settings: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("additional YAML document in Telegram settings")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decoding trailing Telegram settings: %w", err)
	}
	if settings.ChatID == 0 {
		return errors.New("telegram settings chat_id is required")
	}
	if settings.ChatID != t.chatID {
		return fmt.Errorf("telegram settings chat ID %d does not match configured chat ID %d", settings.ChatID, t.chatID)
	}
	t.settings = settings
	return nil
}

func (t *TelegramOutput) settingsSnapshot() notificationSettings {
	t.settingsMu.RLock()
	defer t.settingsMu.RUnlock()
	if t.settings == (notificationSettings{}) {
		return defaultNotificationSettings()
	}
	return t.settings
}

func (t *TelegramOutput) notificationsEnabled(typ string) bool {
	settings := t.settingsSnapshot()
	switch typ {
	case event.TypeBlock:
		return settings.Blocks
	case event.TypeTransaction:
		return settings.Transactions
	case event.TypeRollback:
		return settings.Rollbacks
	case event.TypeGovernance:
		return settings.Governance
	default:
		return true
	}
}

func (t *TelegramOutput) setNotification(kind string, enabled bool) error {
	if t.settingsFile == "" {
		return errors.New("configure output.telegram.settings-file to save notification preferences")
	}
	t.settingsMu.Lock()
	defer t.settingsMu.Unlock()
	settings := t.settings
	switch kind {
	case "blocks":
		settings.Blocks = enabled
	case "transactions":
		settings.Transactions = enabled
	case "rollbacks":
		settings.Rollbacks = enabled
	case "governance":
		settings.Governance = enabled
	default:
		return errors.New("unknown notification type")
	}
	settings.ChatID = t.chatID
	if err := config.SaveAtomic(t.settingsFile, settings); err != nil {
		return fmt.Errorf("saving Telegram settings: %w", err)
	}
	t.settings = settings
	return nil
}

// SetStatusProvider supplies the pipeline running state shown by /status.
// Configure it before starting the Telegram output.
func (t *TelegramOutput) SetStatusProvider(provider func() bool) {
	t.statusProvider = provider
}

func (t *TelegramOutput) canChangeSettings(msg *models.Message) bool {
	if msg.From == nil || msg.From.IsBot {
		return false
	}
	if msg.Chat.Type == models.ChatTypePrivate {
		return msg.From.ID == t.chatID
	}
	_, ok := t.operatorIDs[msg.From.ID]
	return ok
}

func (t *TelegramOutput) commandHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil || update.Message.Text == "" ||
		t.chatID == 0 || update.Message.Chat.ID != t.chatID {
		return
	}
	msg := update.Message
	fields := strings.Fields(msg.Text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return
	}
	command, mention, hasMention := strings.Cut(strings.TrimPrefix(fields[0], "/"), "@")
	if hasMention && (t.botUsername == "" || !strings.EqualFold(mention, t.botUsername)) {
		return
	}
	var reply string
	switch command {
	case "start":
		reply = "This bot sends Adder event notifications. Use /help for commands."
	case "help":
		reply = "Commands: /status, /settings, /notify blocks|transactions|rollbacks|governance on|off."
	case "status":
		state := "unavailable"
		if t.statusProvider != nil {
			if t.statusProvider() {
				state = "running"
			} else {
				state = "stopped"
			}
		}
		reply = "Adder pipeline: " + state
	case "settings":
		s := t.settingsSnapshot()
		reply = fmt.Sprintf("Notifications: blocks %s, transactions %s, rollbacks %s, governance %s.",
			onOff(s.Blocks), onOff(s.Transactions), onOff(s.Rollbacks), onOff(s.Governance))
		if t.settingsFile == "" {
			reply += " Configure output.telegram.settings-file to enable changes."
		}
	case "notify":
		if len(fields) != 3 || (fields[2] != "on" && fields[2] != "off") {
			reply = "Usage: /notify blocks|transactions|rollbacks|governance on|off"
		} else if !t.canChangeSettings(msg) {
			reply = "Only the configured chat owner or operator can change notifications."
		} else if !notificationType(fields[1]) {
			reply = "Usage: /notify blocks|transactions|rollbacks|governance on|off"
		} else if t.settingsFile == "" {
			reply = "Configure output.telegram.settings-file to enable changes."
		} else if err := t.setNotification(fields[1], fields[2] == "on"); err != nil {
			t.log().Warn("failed to save Telegram notification preferences: " + err.Error())
			reply = "Could not save notification preferences."
		} else {
			reply = fields[1] + " notifications " + fields[2] + "."
		}
	default:
		return
	}
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: t.chatID, Text: reply}); err != nil {
		t.log().Warn("failed to send Telegram command response: " + err.Error())
	}
}

func notificationType(kind string) bool {
	switch kind {
	case "blocks", "transactions", "rollbacks", "governance":
		return true
	default:
		return false
	}
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
