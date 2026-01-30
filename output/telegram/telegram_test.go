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
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
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
		for i := 0; i < 500; i++ {
			msg += "aaaaaaaaaa" // 5000 chars
		}
		result := truncateMessage(msg, 4096)
		assert.LessOrEqual(t, len(result), 4096)
		assert.Contains(t, result, "… [truncated]")
		assert.True(t, len(result) < len(msg))
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

	result := formatBlockMessage(be, bc, "https://cexplorer.io")

	assert.Contains(t, result, "New Cardano Block")
	assert.Contains(t, result, "Conway")
	assert.Contains(t, result, "12345")
	assert.Contains(t, result, "67890")
	assert.Contains(t, result, "42")
	assert.Contains(t, result, "1024")
}

func TestFormatRollbackMessage(t *testing.T) {
	re := event.RollbackEvent{
		BlockHash:  "abcdef1234567890",
		SlotNumber: 12345,
	}

	result := formatRollbackMessage(re)

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

	result := formatTransactionMessage(te, tc, "https://cexplorer.io")

	assert.Contains(t, result, "New Cardano Transaction")
	assert.Contains(t, result, "100")
	assert.Contains(t, result, "200")
	assert.Contains(t, result, "0.200000") // Fee in ADA
}

func TestProcessEventInvalidPayloadNoPanic(t *testing.T) {
	tg := &TelegramOutput{}

	// Wrong payload type for block — should log and return, not panic
	evt := &event.Event{
		Type:    "chainsync.block",
		Payload: "not a BlockEvent",
		Context: event.BlockContext{
			Era: "Conway", BlockNumber: 1, SlotNumber: 1, NetworkMagic: mainnetNetworkMagic,
		},
	}
	tg.processEvent(evt)

	// Wrong payload type for rollback
	evt2 := &event.Event{Type: "chainsync.rollback", Payload: 12345}
	tg.processEvent(evt2)

	// Unknown event type
	evt3 := &event.Event{Type: "unknown.type", Payload: "x"}
	tg.processEvent(evt3)
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

	t.Run("WithDisableLinkPreview", func(t *testing.T) {
		tg := &TelegramOutput{}
		WithDisableLinkPreview(true)(tg)
		assert.True(t, tg.disablePreview)
	})
}
