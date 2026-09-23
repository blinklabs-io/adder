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
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingLogger is a plugin.Logger that captures Error messages, so a
// test can observe that a worker actually processed an event instead of
// sleeping and hoping.
type recordingLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *recordingLogger) Info(string, ...any)  {}
func (l *recordingLogger) Warn(string, ...any)  {}
func (l *recordingLogger) Debug(string, ...any) {}

func (l *recordingLogger) Error(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) errorMessages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.messages)
}

// Cardano network magics used by the tests as BlockContext fixtures and to
// exercise getBaseURL's mapping.
const (
	mainnetNetworkMagic uint32 = 764824073
	previewNetworkMagic uint32 = 2
	preprodNetworkMagic uint32 = 1
)

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		options     []TelegramOptionFunc
		expectError bool
		errorMsg    string
	}{
		{
			name:        "no bot token returns error",
			options:     []TelegramOptionFunc{},
			expectError: true,
			errorMsg:    "telegram bot token is required",
		},
		{
			name: "invalid bot token returns error",
			options: []TelegramOptionFunc{
				WithBotToken("invalid-token"),
			},
			expectError: true,
			// The Telegram library will validate the token format
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output, err := New(tc.options...)
			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
				assert.Nil(t, output)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, output)
			}
		})
	}
}

func TestFormatFunctions(t *testing.T) {
	t.Run("truncateHash empty string", func(t *testing.T) {
		result := truncateHash("")
		assert.Equal(t, "", result)
	})

	t.Run("truncateHash short hash", func(t *testing.T) {
		result := truncateHash("abcd1234")
		assert.Equal(t, "abcd1234", result)
	})

	t.Run("truncateHash exactly 16 chars", func(t *testing.T) {
		result := truncateHash("0123456789abcdef")
		assert.Equal(t, "0123456789abcdef", result)
	})

	t.Run("truncateHash long hash", func(t *testing.T) {
		hash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
		result := truncateHash(hash)
		assert.Equal(t, "abcdef12...34567890", result)
	})

	t.Run("formatLovelace", func(t *testing.T) {
		result := formatLovelace(1_500_000)
		assert.Equal(t, "1.500000", result)
	})

	t.Run("getBaseURL mainnet", func(t *testing.T) {
		result := getBaseURL(mainnetNetworkMagic)
		assert.Equal(t, "https://cexplorer.io", result)
	})

	t.Run("getBaseURL preprod", func(t *testing.T) {
		result := getBaseURL(preprodNetworkMagic)
		assert.Equal(t, "https://preprod.cexplorer.io", result)
	})

	t.Run("getBaseURL preview", func(t *testing.T) {
		result := getBaseURL(previewNetworkMagic)
		assert.Equal(t, "https://preview.cexplorer.io", result)
	})

	t.Run("getBaseURL unknown defaults to mainnet", func(t *testing.T) {
		result := getBaseURL(12345)
		assert.Equal(t, "https://cexplorer.io", result)
	})
}

func TestTruncateMessage(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		result := truncateMessage("", 100)
		assert.Equal(t, "", result)
	})

	t.Run("short string unchanged", func(t *testing.T) {
		msg := "short"
		result := truncateMessage(msg, 100)
		assert.Equal(t, msg, result)
	})

	t.Run("exactly at limit unchanged", func(t *testing.T) {
		msg := "x"
		for len(msg) < 4096 {
			msg += "x"
		}
		result := truncateMessage(msg, 4096)
		assert.Equal(t, msg, result)
	})

	t.Run("over limit truncated with suffix", func(t *testing.T) {
		msg := ""
		for range 500 {
			msg += "aaaaaaaaaa" // 5000 chars
		}
		result := truncateMessage(msg, 4096)
		assert.LessOrEqual(
			t,
			utf16Len(result),
			4096,
			"result must be within Telegram UTF-16 limit",
		)
		assert.Contains(t, result, "… [truncated]")
		assert.True(t, utf16Len(result) < utf16Len(msg))
	})

	t.Run("maxLen zero returns as-is", func(t *testing.T) {
		msg := "hello"
		result := truncateMessage(msg, 0)
		assert.Equal(t, msg, result)
	})

	t.Run("maxLen negative returns as-is", func(t *testing.T) {
		msg := "hello"
		result := truncateMessage(msg, -1)
		assert.Equal(t, msg, result)
	})
}

func TestFormatBlockMessage(t *testing.T) {
	be := event.BlockEvent{
		BlockHash:        "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		IssuerVkey:       "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		TransactionCount: 42,
		BlockBodySize:    1024,
	}
	bc := event.BlockContext{
		Era:          "Conway",
		BlockNumber:  12345,
		SlotNumber:   67890,
		NetworkMagic: mainnetNetworkMagic,
	}

	result := formatBlockMessage(
		be,
		bc,
		"https://cexplorer.io",
		models.ParseModeHTML,
	)

	assert.Contains(t, result, "New Cardano Block")
	assert.Contains(t, result, "Conway")
	assert.Contains(t, result, "12345")
	assert.Contains(t, result, "67890")
	assert.Contains(t, result, "42")
	assert.Contains(t, result, "1024")
	assert.Contains(t, result, "<b>") // HTML bold
	assert.Contains(t, result, "<a href=")
}

func TestFormatBlockMessageMarkdown(t *testing.T) {
	be := event.BlockEvent{
		BlockHash:        "abc",
		IssuerVkey:       "def",
		TransactionCount: 1,
		BlockBodySize:    100,
	}
	bc := event.BlockContext{
		Era: "Conway", BlockNumber: 1, SlotNumber: 1, NetworkMagic: mainnetNetworkMagic,
	}
	result := formatBlockMessage(
		be,
		bc,
		"https://cexplorer.io",
		models.ParseModeMarkdownV1,
	)
	assert.Contains(
		t,
		result,
		"*Block Number:*",
	) // Markdown bold (single asterisk per Telegram API)
	assert.Contains(t, result, "](https") // Markdown link
}

func TestEscapeMarkdownV2(t *testing.T) {
	assert.Equal(t, `\.`, escapeMarkdownV2("."))
	assert.Equal(t, `1\.500000`, escapeMarkdownV2("1.500000"))
	assert.Equal(t, `abc\.\.\.xyz`, escapeMarkdownV2("abc...xyz"))
	assert.Equal(t, `\(test\)`, escapeMarkdownV2("(test)"))
}

func TestEscapeMarkdownV2URL(t *testing.T) {
	// Only \ and ) must be escaped in URL; dots and other chars stay
	assert.Equal(
		t,
		"https://example.com/path",
		escapeMarkdownV2URL("https://example.com/path"),
	)
	assert.Equal(
		t,
		`https://example.com/path\)`,
		escapeMarkdownV2URL("https://example.com/path)"),
	)
	// One backslash in URL becomes two in output (escaped for MarkdownV2)
	assert.Equal(
		t,
		`https://example.com/path\\`,
		escapeMarkdownV2URL("https://example.com/path\\"),
	)
}

func TestEscapeForMode(t *testing.T) {
	assert.Equal(t, "Conway", escapeForMode("Conway", models.ParseModeHTML))
	assert.Equal(
		t,
		"Conway",
		escapeForMode("Conway", models.ParseModeMarkdownV1),
	)
	assert.Equal(
		t,
		`1\.500000`,
		escapeForMode("1.500000", models.ParseModeMarkdown),
	)
}

func TestFormatBlockMessageMarkdownV2(t *testing.T) {
	be := event.BlockEvent{
		BlockHash:        "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		IssuerVkey:       "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		TransactionCount: 1,
		BlockBodySize:    100,
	}
	bc := event.BlockContext{
		Era: "Conway", BlockNumber: 1, SlotNumber: 1, NetworkMagic: mainnetNetworkMagic,
	}
	result := formatBlockMessage(
		be,
		bc,
		"https://cexplorer.io",
		models.ParseModeMarkdown,
	)
	assert.Contains(
		t,
		result,
		"*Block Number:*",
	) // MarkdownV2 bold (single asterisk)
	assert.Contains(t, result, "](https") // link with unescaped URL
	assert.Contains(
		t,
		result,
		`\.\.\.`,
	) // truncated hash has escaped dots
	assert.Contains(
		t,
		result,
		"Conway",
	) // Era not escaped for display (Era has no special chars in "Conway")
}

func TestFormatRollbackMessageMarkdownV2(t *testing.T) {
	re := event.RollbackEvent{
		BlockHash:  "abcdef1234567890abcdef1234567890",
		SlotNumber: 12345,
	}
	result := formatRollbackMessage(re, models.ParseModeMarkdown)
	assert.Contains(
		t,
		result,
		"*Slot Number:*",
	) // MarkdownV2 bold (single asterisk)
	assert.Contains(t, result, `\.\.\.`) // truncated hash
}

func TestFormatTransactionMessageMarkdownV2(t *testing.T) {
	te := event.TransactionEvent{Fee: 200_000}
	tc := event.TransactionContext{
		TransactionHash: "txhash1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		BlockNumber:     100, SlotNumber: 200, NetworkMagic: mainnetNetworkMagic,
	}
	result := formatTransactionMessage(
		te,
		tc,
		"https://cexplorer.io",
		models.ParseModeMarkdown,
	)
	assert.Contains(
		t,
		result,
		"*Transaction Hash:*",
	) // MarkdownV2 bold (single asterisk)
	assert.Contains(
		t,
		result,
		`0\.200000`,
	) // Fee in ADA with escaped dot
}

func TestFormatRollbackMessage(t *testing.T) {
	re := event.RollbackEvent{
		BlockHash:  "abcdef1234567890",
		SlotNumber: 12345,
	}

	result := formatRollbackMessage(re, models.ParseModeHTML)

	assert.Contains(t, result, "Rollback")
	assert.Contains(t, result, "12345")
	assert.Contains(t, result, "abcdef1234567890")
}

func TestFormatTransactionMessage(t *testing.T) {
	te := event.TransactionEvent{
		Fee: 200_000,
	}
	tc := event.TransactionContext{
		TransactionHash: "txhash1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		BlockNumber:     100,
		SlotNumber:      200,
		NetworkMagic:    mainnetNetworkMagic,
	}

	result := formatTransactionMessage(
		te,
		tc,
		"https://cexplorer.io",
		models.ParseModeHTML,
	)

	assert.Contains(t, result, "New Cardano Transaction")
	assert.Contains(t, result, "100")
	assert.Contains(t, result, "200")
	assert.Contains(t, result, "0.200000") // Fee in ADA
}

func TestProcessEventInvalidPayloadNoPanic(t *testing.T) {
	tg := &TelegramOutput{}

	// Wrong payload type for block — should log and return, not panic
	evt := &event.Event{
		Type:    event.TypeBlock,
		Payload: "not a BlockEvent",
		Context: event.BlockContext{
			Era: "Conway", BlockNumber: 1, SlotNumber: 1, NetworkMagic: mainnetNetworkMagic,
		},
	}
	tg.processEvent(context.Background(), evt)

	// Wrong payload type for rollback
	evt2 := &event.Event{Type: event.TypeRollback, Payload: 12345}
	tg.processEvent(context.Background(), evt2)

	// Unknown event type
	evt3 := &event.Event{Type: "unknown.type", Payload: "x"}
	tg.processEvent(context.Background(), evt3)
}

func TestWithOptions(t *testing.T) {
	t.Run("WithChatID", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithChatID(12345)(tg)
		assert.Equal(t, int64(12345), tg.chatID)
	})

	t.Run("WithChatID negative (group)", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithChatID(-1001234567890)(tg)
		assert.Equal(t, int64(-1001234567890), tg.chatID)
	})

	t.Run("WithParseMode HTML", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithParseMode("HTML")(tg)
		assert.Equal(t, "HTML", string(tg.parseMode))
	})

	t.Run("WithParseMode Markdown", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithParseMode("Markdown")(tg)
		assert.Equal(t, "Markdown", string(tg.parseMode))
	})

	t.Run("WithParseMode MarkdownV2", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithParseMode("MarkdownV2")(tg)
		assert.Equal(t, "MarkdownV2", string(tg.parseMode))
	})

	t.Run("WithDisableLinkPreview", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithDisableLinkPreview(true)(tg)
		assert.True(t, tg.disablePreview)
	})
}

// TestStopReturnsWhileTheEventWorkerIsParked guards the shutdown deadlock
// the plugin.Base conversion fixed. Stop used to close the done channel and
// then nil the field before waiting for its workers, so an event worker that
// entered its select after the assignment received on a nil channel and
// never woke: Stop's wait blocked forever.
//
// The plugin is built by hand rather than with New: New calls getMe against
// the live Telegram API, and Start would too, so neither can run offline.
func TestStopReturnsWhileTheEventWorkerIsParked(t *testing.T) {
	logger := &recordingLogger{}
	tg := &TelegramOutput{chatID: 1}
	tg.SetLogger(logger)
	plugintest.StartBase(t, &tg.Base, plugin.BaseConfig{HasInput: true})
	done, in := tg.Done(), tg.Input()
	tg.Go(func() { tg.eventLoop(tg.Context(), done, in) })

	// Push one event through first so the worker completes an iteration
	// and re-enters its select: re-entering is the precise condition the
	// old bug needed, and a worker parked on its very first select could
	// not exercise it. processEvent logs and returns on a nil payload, so
	// this reaches no bot.
	tg.InputChan() <- event.Event{Type: event.TypeBlock}
	require.Eventually(t, func() bool {
		return slices.Contains(logger.errorMessages(), "event has nil payload")
	}, 5*time.Second, 10*time.Millisecond,
		"the event worker never consumed the event")

	stopped := make(chan error, 1)
	go func() { stopped <- tg.Stop() }()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal(
			"Stop did not return: the event worker is parked on a nil done channel",
		)
	}

	// The done channel stays closed in place after shutdown, so even a
	// worker that re-read it late would return instead of parking.
	select {
	case <-tg.Done():
	default:
		t.Fatal("Done must stay closed after Stop, not become nil")
	}
}
