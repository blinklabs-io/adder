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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"
)

type telegramCall struct {
	method string
	body   map[string]any
}

type fakeTelegramClient struct {
	calls    chan telegramCall
	updates  chan string
	failSend bool
}

func (c *fakeTelegramClient) Do(r *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(r.URL.Path, "/bot123:abc/")
	var update string
	if method == "getUpdates" {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case update = <-c.updates:
		case <-time.After(10 * time.Millisecond):
		}
	}
	body := make(map[string]any)
	if r.Header.Get("Content-Type") != "" {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		for key, values := range r.MultipartForm.Value {
			if len(values) == 0 {
				continue
			}
			var decoded any
			if json.Unmarshal([]byte(values[0]), &decoded) == nil {
				body[key] = decoded
			} else {
				body[key] = values[0]
			}
		}
	}
	if method != "getUpdates" {
		c.calls <- telegramCall{method: method, body: body}
	}
	var response string
	switch method {
	case "getMe":
		response = `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"Test","username":"adder_test_bot"}}`
	case "setMyCommands":
		response = `{"ok":true,"result":true}`
	case "getUpdates":
		if update == "" {
			response = `{"ok":true,"result":[]}`
		} else {
			response = fmt.Sprintf(`{"ok":true,"result":[%s]}`, update)
		}
	case "sendMessage":
		if c.failSend {
			response = `{"ok":false,"error_code":429,"description":"Too Many Requests"}`
		} else {
			response = `{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":99,"type":"private"},"text":"ok"}}`
		}
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("unknown method"))}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response)),
	}, nil
}

func testTelegramAPI(t *testing.T) (*bot.Bot, chan telegramCall, *fakeTelegramClient) {
	t.Helper()
	calls := make(chan telegramCall, 32)
	client := &fakeTelegramClient{calls: calls, updates: make(chan string, 1)}
	b, err := bot.New("123:abc", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, client))
	require.NoError(t, err)
	return b, calls, client
}

func updateFor(chatID, userID int64, chatType models.ChatType, command string) *models.Update {
	return &models.Update{Message: &models.Message{
		Chat: models.Chat{ID: chatID, Type: chatType},
		From: &models.User{ID: userID}, Text: command,
	}}
}

func nextTelegramCall(t *testing.T, calls chan telegramCall, method string) telegramCall {
	t.Helper()
	select {
	case call := <-calls:
		require.Equal(t, method, call.method)
		return call
	default:
		t.Fatalf("expected %s request", method)
		return telegramCall{}
	}
}

func TestTelegramCommandsMatchBotAPIAndRestrictChats(t *testing.T) {
	b, calls, _ := testTelegramAPI(t)
	tg, err := New(WithBotToken("123:abc"), WithChatID(99))
	require.NoError(t, err)
	tg.bot = b
	tg.botUsername = "adder_test_bot"
	tg.SetStatusProvider(func() bool { return true })
	ctx := context.Background()

	tg.commandHandler(ctx, b, updateFor(100, 100, models.ChatTypePrivate, "/status"))
	tg.commandHandler(ctx, b, updateFor(99, 99, models.ChatTypePrivate, "/status@other_bot"))
	require.Empty(t, calls, "unconfigured chats and other bot mentions must not receive replies")

	tg.commandHandler(ctx, b, updateFor(99, 99, models.ChatTypePrivate, "/status@adder_test_bot"))
	call := nextTelegramCall(t, calls, "sendMessage")
	require.EqualValues(t, 99, call.body["chat_id"])
	require.Equal(t, "Adder pipeline: running", call.body["text"])

	tg.commandHandler(ctx, b, updateFor(99, 99, models.ChatTypePrivate, "/settings"))
	call = nextTelegramCall(t, calls, "sendMessage")
	require.Contains(t, call.body["text"], "blocks on, transactions on, rollbacks on, governance on")
	require.Contains(t, call.body["text"], "settings-file")
}

func TestTelegramPreferencesPersistAndGateDelivery(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "telegram.yaml")
	b, calls, _ := testTelegramAPI(t)
	tg, err := New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.NoError(t, err)
	tg.bot = b
	ctx := context.Background()

	tg.commandHandler(ctx, b, updateFor(99, 99, models.ChatTypePrivate, "/notify rollbacks off"))
	call := nextTelegramCall(t, calls, "sendMessage")
	require.Equal(t, "rollbacks notifications off.", call.body["text"])
	tg.processEvent(ctx, &event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{SlotNumber: 1}})
	require.Empty(t, calls, "disabled rollback events must not reach sendMessage")

	restarted, err := New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.NoError(t, err)
	require.False(t, restarted.settingsSnapshot().Rollbacks, "saved preference must survive reconstruction")
	restarted.bot = b
	restarted.processEvent(ctx, &event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{SlotNumber: 2}})
	require.Empty(t, calls)

	restarted.commandHandler(ctx, b, updateFor(99, 99, models.ChatTypePrivate, "/notify rollbacks on"))
	nextTelegramCall(t, calls, "sendMessage")
	restarted.processEvent(ctx, &event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{SlotNumber: 3}})
	call = nextTelegramCall(t, calls, "sendMessage")
	require.EqualValues(t, 99, call.body["chat_id"])
	require.Contains(t, call.body["text"], "Rollback")
}

func TestTelegramGroupOperatorsControlPreferences(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "group.yaml")
	b, calls, _ := testTelegramAPI(t)
	tg, err := New(WithBotToken("123:abc"), WithChatID(-1001), WithSettingsFile(settingsFile), WithOperatorUserIDs(42))
	require.NoError(t, err)
	tg.bot = b
	ctx := context.Background()

	tg.commandHandler(ctx, b, updateFor(-1001, 7, models.ChatTypeSupergroup, "/notify blocks off"))
	call := nextTelegramCall(t, calls, "sendMessage")
	require.Contains(t, call.body["text"], "Only the configured chat owner or operator")
	require.True(t, tg.settingsSnapshot().Blocks)

	tg.commandHandler(ctx, b, updateFor(-1001, 42, models.ChatTypeSupergroup, "/notify blocks off"))
	nextTelegramCall(t, calls, "sendMessage")
	require.False(t, tg.settingsSnapshot().Blocks)

	tg.commandHandler(ctx, b, updateFor(-1002, 42, models.ChatTypeSupergroup, "/settings"))
	require.Empty(t, calls, "an operator in another chat must not receive a response")
}

func TestTelegramRegistersCommandsForConfiguredChat(t *testing.T) {
	b, calls, _ := testTelegramAPI(t)
	tg, err := New(WithBotToken("123:abc"), WithChatID(99))
	require.NoError(t, err)
	tg.bot = b
	require.NoError(t, tg.Start())
	t.Cleanup(func() { require.NoError(t, tg.Stop()) })
	nextTelegramCall(t, calls, "getMe")
	call := nextTelegramCall(t, calls, "setMyCommands")
	scope, ok := call.body["scope"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "chat", scope["type"])
	require.EqualValues(t, 99, scope["chat_id"])
	commands, ok := call.body["commands"].([]any)
	require.True(t, ok)
	require.Len(t, commands, 5)
}

func TestTelegramPollsBotAPIUpdate(t *testing.T) {
	_, calls, client := testTelegramAPI(t)
	client.updates <- `{"update_id":1,"message":{"message_id":2,"date":1,"chat":{"id":99,"type":"private"},"from":{"id":99,"is_bot":false,"first_name":"Owner"},"text":"/status"}}`
	tg, err := New(WithBotToken("123:abc"), WithChatID(99))
	require.NoError(t, err)
	tg.bot, err = bot.New("123:abc", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, client), bot.WithDefaultHandler(tg.commandHandler))
	require.NoError(t, err)
	tg.SetStatusProvider(func() bool { return true })
	require.NoError(t, tg.Start())
	t.Cleanup(func() { require.NoError(t, tg.Stop()) })
	nextTelegramCall(t, calls, "getMe")
	nextTelegramCall(t, calls, "setMyCommands")
	select {
	case call := <-calls:
		require.Equal(t, "sendMessage", call.method)
		require.EqualValues(t, 99, call.body["chat_id"])
		require.Equal(t, "Adder pipeline: running", call.body["text"])
	case <-time.After(2 * time.Second):
		t.Fatal("polled Telegram update did not produce a command response")
	}
}

func TestTelegramSendMessageReportsBotAPIError(t *testing.T) {
	b, calls, client := testTelegramAPI(t)
	client.failSend = true
	tg, err := New(WithBotToken("123:abc"), WithChatID(99))
	require.NoError(t, err)
	tg.bot = b
	require.ErrorContains(t, tg.SendMessage("test"), "Too Many Requests")
	nextTelegramCall(t, calls, "sendMessage")
}

func TestTelegramRejectsSettingsForAnotherChat(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "telegram.yaml")
	tg, err := New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.NoError(t, err)
	require.NoError(t, tg.setNotification("blocks", false))
	_, err = New(WithBotToken("123:abc"), WithChatID(100), WithSettingsFile(settingsFile))
	require.ErrorContains(t, err, "does not match configured chat ID")
}

func TestTelegramRejectsAdditionalSettingsDocument(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "telegram.yaml")
	tg, err := New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.NoError(t, err)
	require.NoError(t, tg.setNotification("blocks", false))
	data, err := os.ReadFile(settingsFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settingsFile, append(data, []byte("---\nchat_id: 99\n")...), 0o600))
	_, err = New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.ErrorContains(t, err, "additional YAML document")
}

func TestTelegramRequiresSettingsChatID(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "telegram.yaml")
	require.NoError(t, os.WriteFile(settingsFile, []byte("blocks: false\n"), 0o600))
	_, err := New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.ErrorContains(t, err, "chat_id is required")
}

func TestTelegramSaveFailureKeepsPreference(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "settings.yaml")
	b, calls, _ := testTelegramAPI(t)
	tg, err := New(WithBotToken("123:abc"), WithChatID(99), WithSettingsFile(settingsFile))
	require.NoError(t, err)
	tg.bot = b
	require.NoError(t, os.Mkdir(settingsFile, 0o700))
	tg.commandHandler(context.Background(), b, updateFor(99, 99, models.ChatTypePrivate, "/notify blocks off"))
	call := nextTelegramCall(t, calls, "sendMessage")
	require.Equal(t, "Could not save notification preferences.", call.body["text"])
	require.True(t, tg.settingsSnapshot().Blocks)
}
