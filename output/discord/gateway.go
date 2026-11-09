// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package discord

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type gatewayEvent struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
}

type gatewaySession struct {
	id, resumeURL string
	sequence      int64
	hasSequence   bool
	established   bool
}

func validateGatewayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil {
		return errors.New("discord Gateway must provide a WSS URL")
	}
	return nil
}

func gatewayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return ""
	}
	q := u.Query()
	q.Set("v", "10")
	q.Set("encoding", "json")
	u.RawQuery = q.Encode()
	return u.String()
}

func (d *DiscordOutput) gatewayLoop(ctx context.Context, gateway string) {
	var session gatewaySession
	backoff := time.Second
	for ctx.Err() == nil {
		endpoint := gateway
		if session.id != "" && session.resumeURL != "" {
			endpoint = session.resumeURL
		}
		err := d.runGateway(ctx, endpoint, &session)
		if session.established {
			backoff = time.Second
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				switch closeErr.Code {
				case 4004, 4010, 4011, 4012, 4013, 4014:
					d.Fail(fmt.Errorf("discord Gateway closed with code %d", closeErr.Code))
					return
				case 4007, 4009:
					session = gatewaySession{}
				}
			}
			d.reportError(fmt.Errorf("discord Gateway disconnected: %w", err))
		}
		if err := sleep(ctx, backoff); err != nil {
			return
		}
		if backoff < 15*time.Second {
			backoff *= 2
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
		}
	}
}

func (d *DiscordOutput) runGateway(ctx context.Context, endpoint string, session *gatewaySession) error {
	session.established = false
	if err := validateGatewayURL(endpoint); err != nil {
		return err
	}
	conn, response, err := d.dialer.DialContext(ctx, gatewayURL(endpoint), nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("discord Gateway connection failed: %w", err)
	}
	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()
	defer func() {
		conn.Close()
		d.mu.Lock()
		if d.conn == conn {
			d.conn = nil
		}
		d.mu.Unlock()
	}()
	connectionDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-connectionDone:
		}
	}()
	defer close(connectionDone)
	var writeMu sync.Mutex
	write := func(v any) error { writeMu.Lock(); defer writeMu.Unlock(); return conn.WriteJSON(v) }
	var hello gatewayEvent
	if err := conn.ReadJSON(&hello); err != nil {
		return err
	}
	if hello.Op != 10 {
		return errors.New("discord Gateway did not send Hello")
	}
	var interval struct {
		HeartbeatInterval float64 `json:"heartbeat_interval"`
	}
	if json.Unmarshal(hello.D, &interval) != nil || interval.HeartbeatInterval < 100 {
		return errors.New("invalid Discord heartbeat interval")
	}
	heartbeatEvery := time.Duration(interval.HeartbeatInterval * float64(time.Millisecond))
	if session.id != "" && session.hasSequence {
		if err := write(map[string]any{"op": 6, "d": map[string]any{"token": d.token, "session_id": session.id, "seq": session.sequence}}); err != nil {
			return err
		}
	} else {
		if err := write(map[string]any{"op": 2, "d": map[string]any{"token": d.token, "intents": 0, "properties": map[string]string{"os": "adder", "browser": "adder", "device": "adder"}}}); err != nil {
			return err
		}
	}
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	var sequence atomic.Int64
	var hasSequence atomic.Bool
	sequence.Store(session.sequence)
	hasSequence.Store(session.hasSequence)
	var ackMu sync.Mutex
	acknowledged := true
	go func() {
		defer close(heartbeatDone)
		initialDelay := heartbeatEvery / 2
		if offset, err := rand.Int(rand.Reader, big.NewInt(int64(heartbeatEvery))); err == nil {
			initialDelay = time.Duration(offset.Int64())
		}
		timer := time.NewTimer(initialDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopHeartbeat:
				return
			case <-timer.C:
				ackMu.Lock()
				ack := acknowledged
				acknowledged = false
				ackMu.Unlock()
				if !ack {
					conn.Close()
					return
				}
				var current any
				if hasSequence.Load() {
					current = sequence.Load()
				}
				if write(map[string]any{"op": 1, "d": current}) != nil {
					conn.Close()
					return
				}
				timer.Reset(heartbeatEvery)
			}
		}
	}()
	defer func() { close(stopHeartbeat); conn.Close(); <-heartbeatDone }()
	for {
		var evt gatewayEvent
		if err := conn.ReadJSON(&evt); err != nil {
			return err
		}
		if evt.S != nil {
			session.sequence = *evt.S
			session.hasSequence = true
			sequence.Store(*evt.S)
			hasSequence.Store(true)
		}
		switch evt.Op {
		case 0:
			switch evt.T {
			case "READY":
				var ready struct {
					SessionID        string `json:"session_id"`
					ResumeGatewayURL string `json:"resume_gateway_url"`
				}
				if json.Unmarshal(evt.D, &ready) == nil && validateGatewayURL(ready.ResumeGatewayURL) == nil {
					session.id = ready.SessionID
					session.resumeURL = ready.ResumeGatewayURL
					session.established = true
				}
			case "RESUMED":
				session.established = true
			case "INTERACTION_CREATE":
				select {
				case d.interactionSlots <- struct{}{}:
					raw := append(json.RawMessage(nil), evt.D...)
					d.Go(func() { defer func() { <-d.interactionSlots }(); d.handleInteraction(ctx, raw) })
				default:
					d.reportError(errors.New("discord interaction worker limit reached"))
				}
			}
		case 1:
			var sequence any
			if session.hasSequence {
				sequence = session.sequence
			}
			if err := write(map[string]any{"op": 1, "d": sequence}); err != nil {
				return err
			}
		case 7:
			return nil
		case 9:
			var resumable bool
			if json.Unmarshal(evt.D, &resumable) != nil || !resumable {
				*session = gatewaySession{}
			}
			return nil
		case 11:
			ackMu.Lock()
			acknowledged = true
			ackMu.Unlock()
		}
	}
}

type interaction struct {
	ID            string `json:"id"`
	Token         string `json:"token"`
	ApplicationID string `json:"application_id"`
	GuildID       string `json:"guild_id"`
	ChannelID     string `json:"channel_id"`
	Type          int    `json:"type"`
	Data          struct {
		Name string `json:"name"`
	} `json:"data"`
}

func (d *DiscordOutput) handleInteraction(ctx context.Context, raw json.RawMessage) {
	var item interaction
	if json.Unmarshal(raw, &item) != nil || item.Type != 2 || item.ApplicationID != d.applicationID || item.ID == "" || item.Token == "" {
		return
	}
	if item.GuildID != d.guildID {
		return
	}
	var content string
	if item.ChannelID != d.channelID {
		content = "This command is available only in the configured channel."
	} else {
		switch item.Data.Name {
		case "adder-status":
			content = "Adder Discord output is running."
		case "adder-recent":
			d.mu.Lock()
			recent := append([]string(nil), d.recent...)
			d.mu.Unlock()
			if len(recent) == 0 {
				content = "No selected events have been delivered since startup."
			} else {
				content = "Recent delivered events:\n" + strings.Join(recent, "\n")
			}
		default:
			return
		}
	}
	body, err := json.Marshal(map[string]any{"type": 4, "data": map[string]any{"content": content, "flags": 64, "allowed_mentions": map[string]any{"parse": []string{}}}})
	if err != nil {
		d.reportError(err)
		return
	}
	replyCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	path := "/interactions/" + url.PathEscape(item.ID) + "/" + url.PathEscape(item.Token) + "/callback"
	if _, err := d.request(replyCtx, "POST", path, body, false); err != nil && ctx.Err() == nil {
		d.reportError(fmt.Errorf("respond to Discord command: %w", err))
	}
}
