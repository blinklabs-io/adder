// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package matrix

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const maxRetryAfter = time.Minute

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// MatrixOutput sends selected events as m.text room messages.
type MatrixOutput struct {
	plugin.Base
	homeserver string
	token      string
	rooms      []string
	selected   map[string]bool
	client     *http.Client
	maxRetries int
	prefix     string
	nextID     atomic.Uint64
}

// New creates a Matrix output. StartContext validates the configuration.
func New(options ...Option) *MatrixOutput {
	m := &MatrixOutput{
		selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true},
		client:   &http.Client{Timeout: 15 * time.Second}, maxRetries: 3,
	}
	for _, option := range options {
		option(m)
	}
	return m
}

func selectedTypes(names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, errors.New("event-types must name at least one supported type")
	}
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Matrix event type %q", name)
		}
		selected[typ] = true
	}
	return selected, nil
}

func validateHomeserver(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("homeserver-url must be a valid HTTPS URL without credentials, query, or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	return errors.New("homeserver-url must use HTTPS (HTTP is allowed for loopback testing)")
}

func (m *MatrixOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (m *MatrixOutput) Start() error            { return m.StartContext(context.Background()) }
func (m *MatrixOutput) StartContext(ctx context.Context) error {
	return m.StartRun(ctx, plugin.BaseConfig{HasInput: true}, m.start, plugin.ShutdownHooks{})
}

func (m *MatrixOutput) start(ctx context.Context) error {
	if err := validateHomeserver(m.homeserver); err != nil {
		return err
	}
	if m.token == "" {
		return errors.New("access-token is required")
	}
	if len(m.rooms) == 0 {
		return errors.New("room-ids must contain at least one room")
	}
	for _, room := range m.rooms {
		if !strings.HasPrefix(room, "!") || !strings.Contains(room, ":") || strings.ContainsAny(room, " \t\r\n") {
			return errors.New("room-ids must contain Matrix room IDs")
		}
	}
	if m.client == nil {
		return errors.New("matrix HTTP client is required")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("create Matrix transaction IDs: %w", err)
	}
	m.prefix = hex.EncodeToString(random[:])
	done, in := m.Done(), m.Input()
	m.Go(func() {
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
				m.deliverEvent(ctx, evt)
			}
		}
	})
	return nil
}

func (m *MatrixOutput) deliverEvent(ctx context.Context, evt event.Event) {
	if !m.selected[evt.Type] {
		return
	}
	encoded, err := json.Marshal(evt)
	if err != nil {
		m.reportError(fmt.Errorf("encode Matrix event: %w", err))
		return
	}
	message := fmt.Sprintf("Adder %s\n%s", strings.TrimPrefix(evt.Type, "input."), encoded)
	for i, room := range m.rooms {
		id := fmt.Sprintf("adder-%s-%d", m.prefix, m.nextID.Add(1))
		if err := m.deliver(ctx, room, id, message); err != nil && ctx.Err() == nil {
			m.reportError(fmt.Errorf("matrix room %d: %w", i+1, err))
		}
	}
}

func (m *MatrixOutput) send(ctx context.Context, room, id, message string) (int, string, error) {
	data, err := json.Marshal(struct {
		MsgType string `json:"msgtype"`
		Body    string `json:"body"`
	}{MsgType: "m.text", Body: message})
	if err != nil {
		return 0, "", err
	}
	endpoint := strings.TrimRight(m.homeserver, "/") + "/_matrix/client/v3/rooms/" + url.PathEscape(room) + "/send/m.room.message/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, "", errors.New("invalid Matrix room URL")
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return 0, "", uerr.Err
		}
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, "", err
	}
	if resp.StatusCode == http.StatusOK {
		var result struct {
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal(data, &result); err != nil || result.EventID == "" {
			return 0, "", errors.New("matrix response has no event_id")
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if value := resp.Header.Get("Retry-After"); value != "" {
			return resp.StatusCode, value, nil
		}
		var body struct {
			RetryAfterMS int64 `json:"retry_after_ms"`
		}
		if json.Unmarshal(data, &body) == nil && body.RetryAfterMS > 0 {
			return resp.StatusCode, fmt.Sprintf("%.3f", float64(body.RetryAfterMS)/1000), nil
		}
	}
	return resp.StatusCode, "", nil
}

func (m *MatrixOutput) deliver(ctx context.Context, room, id, message string) error {
	for attempt := 0; attempt <= m.maxRetries; attempt++ {
		status, retryAfter, err := m.send(ctx, room, id, message)
		if status == http.StatusOK && err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if status == http.StatusTooManyRequests {
			if attempt == m.maxRetries {
				return errors.New("matrix rate limit retry budget exhausted")
			}
			delay := time.Second << attempt
			if retryAfter != "" {
				seconds, parseErr := strconv.ParseFloat(retryAfter, 64)
				if parseErr != nil || seconds < 0 || seconds > maxRetryAfter.Seconds() {
					return errors.New("matrix rate limit has invalid or excessive Retry-After")
				}
				delay = time.Duration(seconds * float64(time.Second))
			}
			if err := wait(ctx, delay); err != nil {
				return err
			}
			continue
		}
		if err == nil && status < 500 {
			return fmt.Errorf("matrix returned HTTP %d", status)
		}
		if attempt == m.maxRetries {
			if err != nil {
				return fmt.Errorf("matrix request failed after %d attempts: %w", attempt+1, err)
			}
			return fmt.Errorf("matrix returned HTTP %d after %d attempts", status, attempt+1)
		}
		if err := wait(ctx, time.Second<<attempt); err != nil {
			return err
		}
	}
	return errors.New("matrix retry budget exhausted")
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *MatrixOutput) reportError(err error) {
	logger := m.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.matrix")
	}
	logger.Error(err.Error())
	m.TrySendError(err)
}
func (m *MatrixOutput) Stop() error { return m.Shutdown(plugin.ShutdownHooks{}) }

var _ plugin.ManagedPlugin = (*MatrixOutput)(nil)
