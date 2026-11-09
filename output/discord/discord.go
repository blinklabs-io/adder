// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/output/webhook"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/gorilla/websocket"
)

const apiURL = "https://discord.com/api/v10"

// DiscordOutput posts selected events and handles read-only Discord commands.
type DiscordOutput struct {
	plugin.Base
	token, applicationID, guildID, channelID string
	selected                                 map[string]bool
	client                                   *http.Client
	dialer                                   *websocket.Dialer
	apiBaseURL                               string
	mu                                       sync.Mutex
	conn                                     *websocket.Conn
	recent                                   []string
	cooldown                                 map[string]time.Time
	globalCooldown                           time.Time
	interactionSlots                         chan struct{}
}

// New builds a Discord bot output.
func New(options ...Option) (*DiscordOutput, error) {
	d := &DiscordOutput{selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true}, client: &http.Client{Timeout: 15 * time.Second}, dialer: websocket.DefaultDialer, apiBaseURL: apiURL, cooldown: make(map[string]time.Time), interactionSlots: make(chan struct{}, 16)}
	for _, option := range options {
		option(d)
	}
	if d.token == "" {
		return nil, errors.New("bot-token is required")
	}
	for _, item := range []struct{ name, value string }{{"application-id", d.applicationID}, {"guild-id", d.guildID}, {"channel-id", d.channelID}} {
		n, err := strconv.ParseUint(item.value, 10, 64)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%s must be a positive Discord ID", item.name)
		}
	}
	if d.client == nil || d.dialer == nil {
		return nil, errors.New("discord transports are required")
	}
	return d, nil
}

// Role identifies this plugin as a pipeline output.
func (d *DiscordOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start begins the output under a background context.
func (d *DiscordOutput) Start() error { return d.StartContext(context.Background()) }

// StartContext registers commands and connects the output under ctx.
func (d *DiscordOutput) StartContext(ctx context.Context) error {
	return d.StartRun(ctx, plugin.BaseConfig{HasInput: true}, d.start, plugin.ShutdownHooks{BeforeWait: d.closeConnection})
}

func (d *DiscordOutput) start(ctx context.Context) error {
	d.interactionSlots = make(chan struct{}, 16)
	for _, command := range []struct{ Name, Description string }{{"adder-status", "Show Adder output status"}, {"adder-recent", "Show recent Adder events"}} {
		path := fmt.Sprintf("/applications/%s/guilds/%s/commands", d.applicationID, d.guildID)
		body, err := json.Marshal(map[string]any{"name": command.Name, "description": command.Description, "type": 1})
		if err != nil {
			return err
		}
		if _, err := d.request(ctx, http.MethodPost, path, body, true); err != nil {
			return fmt.Errorf("register Discord command: %w", err)
		}
	}
	data, err := d.request(ctx, http.MethodGet, "/gateway/bot", nil, true)
	if err != nil {
		return fmt.Errorf("get Discord Gateway: %w", err)
	}
	var gateway struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &gateway); err != nil {
		return errors.New("invalid Discord Gateway response")
	}
	if err := validateGatewayURL(gateway.URL); err != nil {
		return err
	}
	done, in := d.Done(), d.Input()
	d.Go(func() { d.gatewayLoop(ctx, gateway.URL) })
	d.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case evt, ok := <-in:
				if !ok {
					return
				}
				d.deliverEvent(ctx, evt)
			}
		}
	})
	return nil
}

func (d *DiscordOutput) deliverEvent(ctx context.Context, evt event.Event) {
	if !d.selected[evt.Type] {
		return
	}
	payload, err := webhook.FormatDiscord(&evt)
	if err != nil {
		d.reportError(fmt.Errorf("format Discord event: %w", err))
		return
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		d.reportError(err)
		return
	}
	if message == nil {
		d.reportError(errors.New("empty Discord event message"))
		return
	}
	message["allowed_mentions"] = map[string]any{"parse": []string{}}
	payload, err = json.Marshal(message)
	if err != nil {
		d.reportError(err)
		return
	}
	if _, err := d.request(ctx, http.MethodPost, "/channels/"+d.channelID+"/messages", payload, true); err != nil {
		if ctx.Err() == nil {
			d.reportError(fmt.Errorf("send Discord event: %w", err))
		}
		return
	}
	summary := eventSummary(evt)
	d.mu.Lock()
	d.recent = append(d.recent, summary)
	if len(d.recent) > 5 {
		d.recent = d.recent[len(d.recent)-5:]
	}
	d.mu.Unlock()
}

func eventSummary(evt event.Event) string {
	switch c := evt.Context.(type) {
	case event.BlockContext:
		return fmt.Sprintf("Block %d at slot %d", c.BlockNumber, c.SlotNumber)
	case event.TransactionContext:
		return fmt.Sprintf("Transaction %s at slot %d", c.TransactionHash, c.SlotNumber)
	case event.GovernanceContext:
		return fmt.Sprintf("Governance at slot %d", c.SlotNumber)
	}
	if r, ok := evt.Payload.(event.RollbackEvent); ok {
		return fmt.Sprintf("Rollback to slot %d", r.SlotNumber)
	}
	return evt.Type
}

func (d *DiscordOutput) reportError(err error) {
	logger := d.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.discord")
	}
	logger.Error(err.Error())
	d.TrySendError(err)
}

func (d *DiscordOutput) closeConnection() error {
	d.mu.Lock()
	conn := d.conn
	d.conn = nil
	d.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// Stop closes the Gateway connection and waits for workers.
func (d *DiscordOutput) Stop() error {
	return d.Shutdown(plugin.ShutdownHooks{BeforeWait: d.closeConnection})
}

var _ plugin.ManagedPlugin = (*DiscordOutput)(nil)
