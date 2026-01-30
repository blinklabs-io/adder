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
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	mainnetNetworkMagic uint32 = 764824073
	previewNetworkMagic uint32 = 2
	preprodNetworkMagic uint32 = 1

	// Default retry configuration
	defaultMaxRetries     = 3
	defaultInitialBackoff = 1 * time.Second
	defaultMaxBackoff     = 30 * time.Second
	defaultBackoffFactor  = 2.0

	// telegramMaxMessageLength is the Telegram API limit for message text (UTF-16 code units).
	// We use 4096 to stay within the limit; Telegram uses UTF-16 for counting.
	telegramMaxMessageLength = 4096
)

// TelegramOutput implements the Plugin interface for sending events to Telegram
type TelegramOutput struct {
	errorChan      chan error
	eventChan      chan event.Event
	doneChan       chan struct{}
	wg             sync.WaitGroup
	logger         plugin.Logger
	bot            *bot.Bot
	botToken       string
	chatID         int64
	parseMode      models.ParseMode
	disablePreview bool
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	backoffFactor  float64
}

// New creates a new TelegramOutput with the provided options
func New(options ...TelegramOptionFunc) (*TelegramOutput, error) {
	t := &TelegramOutput{
		parseMode:      models.ParseModeHTML,
		disablePreview: false,
		maxRetries:     defaultMaxRetries,
		initialBackoff: defaultInitialBackoff,
		maxBackoff:     defaultMaxBackoff,
		backoffFactor:  defaultBackoffFactor,
	}
	for _, option := range options {
		option(t)
	}

	// Validate required configuration
	if t.botToken == "" {
		return nil, errors.New("telegram bot token is required")
	}

	b, err := bot.New(t.botToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}
	t.bot = b

	return t, nil
}

// log returns the plugin logger, or the global logger if unset.
func (t *TelegramOutput) log() plugin.Logger {
	if t.logger != nil {
		return t.logger
	}
	return logging.GetLogger()
}

// Start the Telegram output
func (t *TelegramOutput) Start() error {
	// Guard against double-start: wait for existing goroutine to exit
	if t.doneChan != nil {
		close(t.doneChan)
		t.wg.Wait()
	}

	t.eventChan = make(chan event.Event, 10)
	t.errorChan = make(chan error)
	t.doneChan = make(chan struct{})

	logger := t.log()
	logger.Info("starting Telegram output")

	if t.chatID == 0 {
		return errors.New("chat ID is required: set --output-telegram-chat-id or OUTPUT_TELEGRAM_CHAT_ID")
	}

	// Verify bot authorization by getting bot info
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	me, err := t.bot.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("failed to authorize with Telegram: %w", err)
	}
	if me.Username != "" {
		logger.Info("Telegram bot authorized as @" + me.Username)
	} else {
		logger.Info("Telegram bot authorized")
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for {
			select {
			case <-t.doneChan:
				return
			case evt, ok := <-t.eventChan:
				// Channel has been closed, which means we're shutting down
				if !ok {
					return
				}
				t.processEvent(&evt)
			}
		}
	}()

	return nil
}

// processEvent handles incoming events and sends them to Telegram
func (t *TelegramOutput) processEvent(evt *event.Event) {
	logger := t.log()

	payload := evt.Payload
	if payload == nil {
		logger.Error("event has nil payload")
		return
	}

	var message string
	switch evt.Type {
	case "chainsync.block":
		evtCtx := evt.Context
		if evtCtx == nil {
			logger.Error("block event has nil context")
			return
		}
		be, ok := payload.(event.BlockEvent)
		if !ok {
			logger.Error("block event has invalid payload type")
			return
		}
		bc, ok := evtCtx.(event.BlockContext)
		if !ok {
			logger.Error("block event has invalid context type")
			return
		}

		baseURL := getBaseURL(bc.NetworkMagic)
		message = formatBlockMessage(be, bc, baseURL)

	case "chainsync.rollback":
		re, ok := payload.(event.RollbackEvent)
		if !ok {
			logger.Error("rollback event has invalid payload type")
			return
		}
		message = formatRollbackMessage(re)

	case "chainsync.transaction":
		evtCtx := evt.Context
		if evtCtx == nil {
			logger.Error("transaction event has nil context")
			return
		}
		te, ok := payload.(event.TransactionEvent)
		if !ok {
			logger.Error("transaction event has invalid payload type")
			return
		}
		tc, ok := evtCtx.(event.TransactionContext)
		if !ok {
			logger.Error("transaction event has invalid context type")
			return
		}

		baseURL := getBaseURL(tc.NetworkMagic)
		message = formatTransactionMessage(te, tc, baseURL)

	default:
		logger.Error("unknown event type: " + evt.Type)
		return
	}

	message = truncateMessage(message, telegramMaxMessageLength)
	t.sendMessageWithRetry(message)
}

// formatBlockMessage formats a block event for Telegram
func formatBlockMessage(be event.BlockEvent, bc event.BlockContext, baseURL string) string {
	return fmt.Sprintf(
		"<b>🧱 New Cardano Block</b>\n\n"+
			"<b>Era:</b> %s\n"+
			"<b>Block Number:</b> %d\n"+
			"<b>Slot Number:</b> %d\n"+
			"<b>Block Hash:</b> <a href=\"%s/block/%s\">%s</a>\n"+
			"<b>Issuer:</b> %s\n"+
			"<b>Transactions:</b> %d\n"+
			"<b>Body Size:</b> %d bytes",
		bc.Era,
		bc.BlockNumber,
		bc.SlotNumber,
		baseURL, be.BlockHash, truncateHash(be.BlockHash),
		truncateHash(be.IssuerVkey),
		be.TransactionCount,
		be.BlockBodySize,
	)
}

// formatRollbackMessage formats a rollback event for Telegram
func formatRollbackMessage(re event.RollbackEvent) string {
	return fmt.Sprintf(
		"<b>⚠️ Cardano Rollback</b>\n\n"+
			"<b>Slot Number:</b> %d\n"+
			"<b>Block Hash:</b> %s",
		re.SlotNumber,
		truncateHash(re.BlockHash),
	)
}

// formatTransactionMessage formats a transaction event for Telegram
func formatTransactionMessage(
	te event.TransactionEvent,
	tc event.TransactionContext,
	baseURL string,
) string {
	return fmt.Sprintf(
		"<b>💳 New Cardano Transaction</b>\n\n"+
			"<b>Block Number:</b> %d\n"+
			"<b>Slot Number:</b> %d\n"+
			"<b>Transaction Hash:</b> <a href=\"%s/tx/%s\">%s</a>\n"+
			"<b>Inputs:</b> %d\n"+
			"<b>Outputs:</b> %d\n"+
			"<b>Fee:</b> %s ADA",
		tc.BlockNumber,
		tc.SlotNumber,
		baseURL, tc.TransactionHash, truncateHash(tc.TransactionHash),
		len(te.Inputs),
		len(te.Outputs),
		formatLovelace(te.Fee),
	)
}

// truncateHash truncates a hash for display
func truncateHash(hash string) string {
	if len(hash) <= 16 {
		return hash
	}
	return hash[:8] + "..." + hash[len(hash)-8:]
}

// truncateMessage ensures text fits within Telegram's message length limit.
// It truncates on rune boundaries and appends "… [truncated]" when shortened.
func truncateMessage(text string, maxLen int) string {
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	suffix := "… [truncated]"
	keep := maxLen - len(suffix)
	if keep <= 0 {
		return text[:maxLen]
	}
	trunc := text[:keep]
	for len(trunc) > 0 && !utf8.ValidString(trunc) {
		trunc = trunc[:len(trunc)-1]
	}
	return trunc + suffix
}

// formatLovelace formats lovelace amount to ADA
func formatLovelace(lovelace uint64) string {
	ada := float64(lovelace) / 1_000_000
	return strconv.FormatFloat(ada, 'f', 6, 64)
}

// getBaseURL returns the block explorer URL based on network magic
func getBaseURL(networkMagic uint32) string {
	switch networkMagic {
	case mainnetNetworkMagic:
		return "https://cexplorer.io"
	case preprodNetworkMagic:
		return "https://preprod.cexplorer.io"
	case previewNetworkMagic:
		return "https://preview.cexplorer.io"
	default:
		return "https://cexplorer.io"
	}
}

// SendMessage sends a message to the configured Telegram chat
func (t *TelegramOutput) SendMessage(message string) error {
	logger := t.log()

	if t.chatID == 0 {
		return errors.New("no chat ID configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	params := &bot.SendMessageParams{
		ChatID:    t.chatID,
		Text:      message,
		ParseMode: t.parseMode,
	}

	// Set link preview options if preview is disabled
	if t.disablePreview {
		params.LinkPreviewOptions = &models.LinkPreviewOptions{
			IsDisabled: bot.True(),
		}
	}

	_, err := t.bot.SendMessage(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	logger.Debug(fmt.Sprintf("Sent message to chat %d", t.chatID))
	return nil
}

// sendMessageWithRetry wraps SendMessage with retry logic and exponential backoff
func (t *TelegramOutput) sendMessageWithRetry(message string) {
	logger := t.log()
	var lastErr error
	backoff := t.initialBackoff

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if attempt > 0 {
			logger.Warn(
				fmt.Sprintf(
					"Telegram delivery failed, retrying (attempt %d/%d) after %v",
					attempt,
					t.maxRetries,
					backoff,
				),
				"chat_id", t.chatID,
				"error", lastErr,
			)
			time.Sleep(backoff)

			// Calculate next backoff with exponential increase
			backoff = time.Duration(float64(backoff) * t.backoffFactor)
			if backoff > t.maxBackoff {
				backoff = t.maxBackoff
			}
		}

		err := t.SendMessage(message)
		if err == nil {
			if attempt > 0 {
				logger.Info(
					fmt.Sprintf("Telegram delivery succeeded after %d retries", attempt),
					"chat_id", t.chatID,
				)
			}
			return
		}
		lastErr = err
	}

	// All retries exhausted
	logger.Error(
		fmt.Sprintf(
			"Telegram delivery failed after %d retries, giving up",
			t.maxRetries,
		),
		"chat_id", t.chatID,
		"error", lastErr,
	)

	// Send error to error channel for monitoring (non-blocking)
	select {
	case t.errorChan <- fmt.Errorf(
		"telegram delivery to chat %d failed after %d retries: %w",
		t.chatID,
		t.maxRetries,
		lastErr,
	):
	default:
		// Error channel is full or closed, just log
		logger.Warn("could not send error to error channel (full or closed)")
	}
}

// Stop the Telegram output
func (t *TelegramOutput) Stop() error {
	if t.doneChan != nil {
		close(t.doneChan)
		t.doneChan = nil
	}
	// Wait for goroutine to exit before closing channels
	t.wg.Wait()
	if t.eventChan != nil {
		close(t.eventChan)
		t.eventChan = nil
	}
	if t.errorChan != nil {
		close(t.errorChan)
		t.errorChan = nil
	}
	return nil
}

// ErrorChan returns the plugin's error channel
func (t *TelegramOutput) ErrorChan() <-chan error {
	return t.errorChan
}

// InputChan returns the input event channel
func (t *TelegramOutput) InputChan() chan<- event.Event {
	return t.eventChan
}

// OutputChan always returns nil
func (t *TelegramOutput) OutputChan() <-chan event.Event {
	return nil
}

// GetBot returns the underlying Telegram bot instance for advanced usage
func (t *TelegramOutput) GetBot() *bot.Bot {
	return t.bot
}

// GetChatID returns the configured chat ID
func (t *TelegramOutput) GetChatID() int64 {
	return t.chatID
}
