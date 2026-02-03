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
	"strings"
	"sync"
	"time"

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
	pollCancel     context.CancelFunc
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

	cmdHandler := commandHandler(t.chatID)
	b, err := bot.New(t.botToken,
		bot.WithDefaultHandler(cmdHandler),
		bot.WithAllowedUpdates(bot.AllowedUpdates{models.AllowedUpdateMessage}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}
	t.bot = b

	return t, nil
}

// commandHandler returns a handler that replies to /start, /help, and /settings (Telegram global commands).
// Replies are sent only in the configured chat (chatID) so the bot stays send-only and minimal.
func commandHandler(chatID int64) func(context.Context, *bot.Bot, *models.Update) {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" {
			return
		}
		if chatID != 0 && update.Message.Chat.ID != chatID {
			return
		}
		text := strings.TrimSpace(update.Message.Text)
		var reply string
		switch {
		case text == "/start" || strings.HasPrefix(text, "/start "):
			reply = "This bot sends Cardano event notifications. Use /help for more."
		case text == "/help":
			reply = "This bot sends Cardano chain event notifications (blocks, transactions, rollbacks) to this chat. It is run by Adder and has no in-chat settings."
		case text == "/settings":
			reply = "No configurable settings."
		default:
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   reply,
		})
	}
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
	// Guard against double-start: stop poll loop, wait for goroutines to exit, then close old channels
	if t.pollCancel != nil {
		t.pollCancel()
		t.pollCancel = nil
	}
	if t.doneChan != nil {
		close(t.doneChan)
		t.doneChan = nil
		t.wg.Wait()
	}
	if t.eventChan != nil {
		close(t.eventChan)
		t.eventChan = nil
	}
	if t.errorChan != nil {
		close(t.errorChan)
		t.errorChan = nil
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

	// Set global commands per Telegram bot requirements (https://core.telegram.org/bots/features#global-commands)
	globalCommands := []models.BotCommand{
		{Command: "start", Description: "Start the bot and see an introduction"},
		{Command: "help", Description: "Show help and list of commands"},
		{Command: "settings", Description: "View bot settings"},
	}
	if _, err := t.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: globalCommands}); err != nil {
		logger.Warn("failed to set Telegram bot commands: " + err.Error())
	}

	// Start long polling so the bot can react to /start, /help, /settings
	pollCtx, pollCancel := context.WithCancel(context.Background())
	t.pollCancel = pollCancel
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.bot.Start(pollCtx)
	}()

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
		message = formatBlockMessage(be, bc, baseURL, t.parseMode)

	case "chainsync.rollback":
		re, ok := payload.(event.RollbackEvent)
		if !ok {
			logger.Error("rollback event has invalid payload type")
			return
		}
		message = formatRollbackMessage(re, t.parseMode)

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
		message = formatTransactionMessage(te, tc, baseURL, t.parseMode)

	default:
		logger.Error("unknown event type: " + evt.Type)
		return
	}

	message = truncateMessage(message, telegramMaxMessageLength)
	t.sendMessageWithRetry(message)
}

// formatBlockMessage formats a block event for Telegram
func formatBlockMessage(be event.BlockEvent, bc event.BlockContext, baseURL string, mode models.ParseMode) string {
	blockURL := baseURL + "/block/" + be.BlockHash
	return fmt.Sprintf(
		"%s\n\n"+
			"%s %s\n"+
			"%s %d\n"+
			"%s %d\n"+
			"%s %s\n"+
			"%s %s\n"+
			"%s %d\n"+
			"%s %d bytes",
		bold("🧱 New Cardano Block", mode),
		bold("Era:", mode), escapeForMode(bc.Era, mode),
		bold("Block Number:", mode), bc.BlockNumber,
		bold("Slot Number:", mode), bc.SlotNumber,
		bold("Block Hash:", mode), link(blockURL, truncateHash(be.BlockHash), mode),
		bold("Issuer:", mode), escapeForMode(truncateHash(be.IssuerVkey), mode),
		bold("Transactions:", mode), be.TransactionCount,
		bold("Body Size:", mode), be.BlockBodySize,
	)
}

// formatRollbackMessage formats a rollback event for Telegram
func formatRollbackMessage(re event.RollbackEvent, mode models.ParseMode) string {
	return fmt.Sprintf(
		"%s\n\n"+
			"%s %d\n"+
			"%s %s",
		bold("⚠️ Cardano Rollback", mode),
		bold("Slot Number:", mode), re.SlotNumber,
		bold("Block Hash:", mode), escapeForMode(truncateHash(re.BlockHash), mode),
	)
}

// formatTransactionMessage formats a transaction event for Telegram
func formatTransactionMessage(
	te event.TransactionEvent,
	tc event.TransactionContext,
	baseURL string,
	mode models.ParseMode,
) string {
	txURL := baseURL + "/tx/" + tc.TransactionHash
	return fmt.Sprintf(
		"%s\n\n"+
			"%s %d\n"+
			"%s %d\n"+
			"%s %s\n"+
			"%s %d\n"+
			"%s %d\n"+
			"%s %s ADA",
		bold("💳 New Cardano Transaction", mode),
		bold("Block Number:", mode), tc.BlockNumber,
		bold("Slot Number:", mode), tc.SlotNumber,
		bold("Transaction Hash:", mode), link(txURL, truncateHash(tc.TransactionHash), mode),
		bold("Inputs:", mode), len(te.Inputs),
		bold("Outputs:", mode), len(te.Outputs),
		bold("Fee:", mode), escapeForMode(formatLovelace(te.Fee), mode),
	)
}

// escapeMarkdownV2 escapes MarkdownV2 special characters: _ * [ ] ( ) ~ ` > # + - = | { } . ! \
// See https://core.telegram.org/bots/api#markdownv2-style
func escapeMarkdownV2(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!', '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeMarkdownV2URL escapes only \ and ) inside a MarkdownV2 link URL.
// In [text](url), the URL must only escape these so the closing ')' is not consumed.
func escapeMarkdownV2URL(url string) string {
	var b strings.Builder
	for _, r := range url {
		if r == '\\' || r == ')' {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeForMode escapes s for MarkdownV2 when mode is ParseModeMarkdown; otherwise returns s unchanged.
func escapeForMode(s string, mode models.ParseMode) string {
	if mode == models.ParseModeMarkdown {
		return escapeMarkdownV2(s)
	}
	return s
}

// bold returns s as bold for the given parse mode.
// Telegram API: Markdown and MarkdownV2 use single asterisk for bold: *bold*
// https://core.telegram.org/bots/api#markdownv2-style
func bold(s string, mode models.ParseMode) string {
	switch mode {
	case models.ParseModeHTML:
		return "<b>" + s + "</b>"
	case models.ParseModeMarkdownV1:
		return "*" + s + "*"
	case models.ParseModeMarkdown:
		return "*" + escapeMarkdownV2(s) + "*"
	default:
		return "<b>" + s + "</b>"
	}
}

// link returns a link for the given parse mode.
// For MarkdownV2: link text is fully escaped; URL is escaped only for \ and ) per Telegram API.
func link(url, text string, mode models.ParseMode) string {
	switch mode {
	case models.ParseModeHTML:
		return fmt.Sprintf("<a href=\"%s\">%s</a>", url, text)
	case models.ParseModeMarkdownV1:
		return fmt.Sprintf("[%s](%s)", text, url)
	case models.ParseModeMarkdown:
		return fmt.Sprintf("[%s](%s)", escapeMarkdownV2(text), escapeMarkdownV2URL(url))
	default:
		return fmt.Sprintf("<a href=\"%s\">%s</a>", url, text)
	}
}

// truncateHash truncates a hash for display
func truncateHash(hash string) string {
	if len(hash) <= 16 {
		return hash
	}
	return hash[:8] + "..." + hash[len(hash)-8:]
}

// utf16Len returns the length of s in UTF-16 code units (Telegram's count).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r < 0x10000 {
			n++
		} else {
			n += 2
		}
	}
	return n
}

// truncateMessage ensures text fits within Telegram's message length limit
// (maxLen is in UTF-16 code units). It truncates on rune boundaries and
// appends "… [truncated]" when shortened.
func truncateMessage(text string, maxLen int) string {
	if maxLen <= 0 {
		return text
	}
	suffix := "… [truncated]"
	suffixUnits := utf16Len(suffix)
	if utf16Len(text) <= maxLen {
		return text
	}
	keepUnits := maxLen - suffixUnits
	if keepUnits <= 0 {
		return suffix
	}
	var runes []rune
	units := 0
	for _, r := range text {
		need := 1
		if r >= 0x10000 {
			need = 2
		}
		if units+need > keepUnits {
			break
		}
		runes = append(runes, r)
		units += need
	}
	return string(runes) + suffix
}

// formatLovelace formats lovelace amount to ADA using integer division so
// large amounts are not rounded by float64.
func formatLovelace(lovelace uint64) string {
	const lovelacePerADA = 1_000_000
	ada := lovelace / lovelacePerADA
	frac := lovelace % lovelacePerADA
	return fmt.Sprintf("%d.%06d", ada, frac)
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

	if t.bot == nil {
		return errors.New("telegram bot not initialized; use telegram.New()")
	}
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
	if t.pollCancel != nil {
		t.pollCancel()
		t.pollCancel = nil
	}
	if t.doneChan != nil {
		close(t.doneChan)
		t.doneChan = nil
	}
	// Wait for goroutines to exit before closing channels
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
