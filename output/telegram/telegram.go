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
	"strings"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/cardanofmt"
	"github.com/blinklabs-io/adder/internal/explorer"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
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
	plugin.Base
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

	parts := strings.SplitN(t.botToken, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, errors.New("invalid telegram bot token format")
	}
	if id, err := strconv.ParseInt(parts[0], 10, 64); err != nil || id <= 0 {
		return nil, errors.New("invalid telegram bot token format")
	}
	cmdHandler := commandHandler(t.chatID)
	b, err := bot.New(t.botToken,
		bot.WithSkipGetMe(),
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
func commandHandler(
	chatID int64,
) func(context.Context, *bot.Bot, *models.Update) {
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
	if logger := t.Logger(); logger != nil {
		return logger
	}
	return logging.GetLoggerForComponent("output.telegram")
}

// Role identifies this plugin as a pipeline output.
func (t *TelegramOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start the Telegram output
func (t *TelegramOutput) Start() error {
	return t.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (t *TelegramOutput) StartContext(ctx context.Context) error {
	return t.StartRun(ctx,
		plugin.BaseConfig{HasInput: true},
		t.start,
		t.shutdownHooks(),
	)
}

func (t *TelegramOutput) start(ctx context.Context) error {
	logger := t.log()
	logger.Info("starting Telegram output")

	if t.chatID == 0 {
		return errors.New(
			"chat ID is required: set --output-telegram-chat-id or OUTPUT_TELEGRAM_CHAT_ID",
		)
	}

	// Verify bot authorization by getting bot info
	authCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	me, err := t.bot.GetMe(authCtx)
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
		{
			Command:     "start",
			Description: "Start the bot and see an introduction",
		},
		{Command: "help", Description: "Show help and list of commands"},
		{Command: "settings", Description: "View bot settings"},
	}
	if _, err := t.bot.SetMyCommands(authCtx, &bot.SetMyCommandsParams{Commands: globalCommands}); err != nil {
		logger.Warn("failed to set Telegram bot commands: " + err.Error())
	}

	// Start long polling so the bot can react to /start, /help, /settings
	pollCtx, pollCancel := context.WithCancel(ctx)
	t.pollCancel = pollCancel
	t.Go(func() { t.bot.Start(pollCtx) })

	// Capture once, outside the loop: a select re-evaluates its channel
	// operands on every entry, so a worker that re-reads the accessors
	// per iteration would park forever if Base's teardown ordering ever
	// changed to clear them. The deadlock this plugin had took that form.
	done, in := t.Done(), t.Input()
	t.Go(func() { t.eventLoop(ctx, done, in) })

	return nil
}

// eventLoop drains the input channel until the plugin shuts down.
func (t *TelegramOutput) eventLoop(
	ctx context.Context,
	done <-chan struct{},
	in <-chan event.Event,
) {
	for {
		select {
		case <-done:
			return
		case evt, ok := <-in:
			// Channel has been closed, which means we're shutting down
			if !ok {
				return
			}
			t.processEvent(ctx, &evt)
		}
	}
}

// processEvent handles incoming events and sends them to Telegram
func (t *TelegramOutput) processEvent(ctx context.Context, evt *event.Event) {
	logger := t.log()

	payload := evt.Payload
	if payload == nil {
		logger.Error("event has nil payload")
		return
	}

	var message string
	switch evt.Type {
	case event.TypeBlock:
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

	case event.TypeRollback:
		re, ok := payload.(event.RollbackEvent)
		if !ok {
			logger.Error("rollback event has invalid payload type")
			return
		}
		message = formatRollbackMessage(re, t.parseMode)

	case event.TypeTransaction:
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

	case event.TypeGovernance:
		evtCtx := evt.Context
		if evtCtx == nil {
			logger.Error("governance event has nil context")
			return
		}
		ge, ok := payload.(event.GovernanceEvent)
		if !ok {
			logger.Error("governance event has invalid payload type")
			return
		}
		gc, ok := evtCtx.(event.GovernanceContext)
		if !ok {
			logger.Error("governance event has invalid context type")
			return
		}

		baseURL := getBaseURL(gc.NetworkMagic)
		message = formatGovernanceMessage(ge, gc, baseURL, t.parseMode)

	default:
		logger.Error("unknown event type: " + evt.Type)
		return
	}

	message = truncateMessage(message, telegramMaxMessageLength)
	t.sendMessageWithRetry(ctx, message)
}

// formatBlockMessage formats a block event for Telegram
func formatBlockMessage(
	be event.BlockEvent,
	bc event.BlockContext,
	baseURL string,
	mode models.ParseMode,
) string {
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
		bold("Era:", mode),
		escapeForMode(bc.Era, mode),
		bold("Block Number:", mode),
		bc.BlockNumber,
		bold("Slot Number:", mode),
		bc.SlotNumber,
		bold(
			"Block Hash:",
			mode,
		),
		link(blockURL, truncateHash(be.BlockHash), mode),
		bold("Issuer:", mode),
		escapeForMode(truncateHash(be.IssuerVkey), mode),
		bold("Transactions:", mode),
		be.TransactionCount,
		bold("Body Size:", mode),
		be.BlockBodySize,
	)
}

// formatRollbackMessage formats a rollback event for Telegram
func formatRollbackMessage(
	re event.RollbackEvent,
	mode models.ParseMode,
) string {
	return fmt.Sprintf(
		"%s\n\n"+
			"%s %d\n"+
			"%s %s",
		bold("⚠️ Cardano Rollback", mode),
		bold("Slot Number:", mode),
		re.SlotNumber,
		bold(
			"Block Hash:",
			mode,
		),
		escapeForMode(truncateHash(re.BlockHash), mode),
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
		bold("Block Number:", mode),
		tc.BlockNumber,
		bold("Slot Number:", mode),
		tc.SlotNumber,
		bold(
			"Transaction Hash:",
			mode,
		),
		link(txURL, truncateHash(tc.TransactionHash), mode),
		bold("Inputs:", mode),
		len(te.Inputs),
		bold("Outputs:", mode),
		len(te.Outputs),
		bold("Fee:", mode),
		escapeForMode(formatLovelace(te.Fee), mode),
	)
}

// formatGovernanceMessage formats a governance event for Telegram
func formatGovernanceMessage(
	ge event.GovernanceEvent,
	gc event.GovernanceContext,
	baseURL string,
	mode models.ParseMode,
) string {
	txURL := baseURL + "/tx/" + gc.TransactionHash
	return fmt.Sprintf(
		"%s\n\n"+
			"%s %d\n"+
			"%s %d\n"+
			"%s %s\n"+
			"%s %d\n"+
			"%s %d\n"+
			"%s %d",
		bold("🏛️ Cardano Governance Event", mode),
		bold("Block Number:", mode),
		gc.BlockNumber,
		bold("Slot Number:", mode),
		gc.SlotNumber,
		bold(
			"Transaction Hash:",
			mode,
		),
		link(txURL, truncateHash(gc.TransactionHash), mode),
		bold("Proposals:", mode),
		len(ge.ProposalProcedures),
		bold("Votes:", mode),
		len(ge.VotingProcedures),
		bold(
			"Certificates:",
			mode,
		),
		len(
			ge.DRepCertificates,
		)+len(
			ge.VoteDelegationCertificates,
		)+len(
			ge.CommitteeCertificates,
		),
	)
}

// escapeMarkdownV2 escapes MarkdownV2 special characters: _ * [ ] ( ) ~ ` > # + - = | { } . ! \
// See https://core.telegram.org/bots/api#markdownv2-style
func escapeMarkdownV2(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '_',
			'*',
			'[',
			']',
			'(',
			')',
			'~',
			'`',
			'>',
			'#',
			'+',
			'-',
			'=',
			'|',
			'{',
			'}',
			'.',
			'!',
			'\\':
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
		return fmt.Sprintf(
			"[%s](%s)",
			escapeMarkdownV2(text),
			escapeMarkdownV2URL(url),
		)
	default:
		return fmt.Sprintf("<a href=\"%s\">%s</a>", url, text)
	}
}

// truncateHash truncates a hash for display
func truncateHash(hash string) string {
	return cardanofmt.TruncateMiddle(hash, 8, 8, "...")
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
	runes := make([]rune, 0, len(text))
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

// formatLovelace formats lovelace amount to ADA using integer division
// so large amounts are not rounded by float64. The shared helper lives
// in internal/cardanofmt so the tray notifications package renders the
// same ADA value as Telegram for the same transaction.
func formatLovelace(lovelace uint64) string {
	return cardanofmt.LovelaceToADA(lovelace)
}

// getBaseURL returns the block explorer URL based on network magic
func getBaseURL(networkMagic uint32) string {
	return explorer.BaseURL(networkMagic)
}

// SendMessage sends a message to the configured Telegram chat
func (t *TelegramOutput) SendMessage(message string) error {
	return t.sendMessage(context.Background(), message)
}

func (t *TelegramOutput) sendMessage(
	ctx context.Context,
	message string,
) error {
	logger := t.log()

	if t.bot == nil {
		return errors.New("telegram bot not initialized; use telegram.New()")
	}
	if t.chatID == 0 {
		return errors.New("no chat ID configured")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
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

	logger.Debug("Sent message to chat", "chat_id", t.chatID)
	return nil
}

// sendMessageWithRetry wraps SendMessage with retry logic and exponential backoff
func (t *TelegramOutput) sendMessageWithRetry(
	ctx context.Context,
	message string,
) {
	logger := t.log()
	var lastErr error
	backoff := t.initialBackoff

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return
		}
		if attempt > 0 {
			logger.Warn(
				"Telegram delivery failed, retrying",
				"attempt",
				attempt,
				"max_retries",
				t.maxRetries,
				"delay",
				backoff,
				"chat_id",
				t.chatID,
				"error",
				lastErr,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			// Calculate next backoff with exponential increase
			backoff = min(
				time.Duration(float64(backoff)*t.backoffFactor),
				t.maxBackoff,
			)
		}

		err := t.sendMessage(ctx, message)
		if err == nil {
			if attempt > 0 {
				logger.Info(
					"Telegram delivery succeeded",
					"retries",
					attempt,
					"chat_id",
					t.chatID,
				)
			}
			return
		}
		lastErr = err
	}

	// All retries exhausted
	logger.Error(
		"Telegram delivery failed, giving up",
		"max_retries",
		t.maxRetries,
		"chat_id",
		t.chatID,
		"error",
		lastErr,
	)

	// Send error to error channel for monitoring (non-blocking)
	if !t.TrySendError(fmt.Errorf(
		"telegram delivery to chat %d failed after %d retries: %w",
		t.chatID,
		t.maxRetries,
		lastErr,
	)) {
		// Error channel is full or closed, just log
		logger.Warn("could not send error to error channel (full or absent)")
	}
}

// Stop the Telegram output
func (t *TelegramOutput) Stop() error {
	return t.Shutdown(t.shutdownHooks())
}

func (t *TelegramOutput) shutdownHooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{
		BeforeWait: func() error {
			// Cancel the long-poll context so bot.Start returns.
			if t.pollCancel != nil {
				t.pollCancel()
				t.pollCancel = nil
			}
			return nil
		},
	}
}

// GetBot returns the underlying Telegram bot instance for advanced usage
func (t *TelegramOutput) GetBot() *bot.Bot {
	return t.bot
}

// GetChatID returns the configured chat ID
func (t *TelegramOutput) GetChatID() int64 {
	return t.chatID
}

var _ plugin.ManagedPlugin = (*TelegramOutput)(nil)
