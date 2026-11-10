// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate, "drep-retirement": event.TypeDRepRetirement,
}

// SignalOutput sends selected events through a local signal-cli JSON-RPC daemon.
type SignalOutput struct {
	plugin.Base
	endpoint   string
	account    string
	recipients []string
	selected   map[string]bool
	client     *http.Client
}

type Option func(*SignalOutput)

func WithEndpoint(v string) Option { return func(s *SignalOutput) { s.endpoint = v } }
func WithAccount(v string) Option  { return func(s *SignalOutput) { s.account = v } }
func WithRecipients(v ...string) Option {
	return func(s *SignalOutput) { s.recipients = append([]string(nil), v...) }
}
func WithEventTypes(v map[string]bool) Option { return func(s *SignalOutput) { s.selected = v } }
func WithHTTPClient(v *http.Client) Option    { return func(s *SignalOutput) { s.client = v } }

func New(opts ...Option) *SignalOutput {
	s := &SignalOutput{endpoint: "http://127.0.0.1:8080/api/v1/rpc", selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true}, client: &http.Client{Timeout: 15 * time.Second}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *SignalOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (s *SignalOutput) Start() error            { return s.StartContext(context.Background()) }
func (s *SignalOutput) StartContext(ctx context.Context) error {
	return s.StartRun(ctx, plugin.BaseConfig{HasInput: true}, s.start, plugin.ShutdownHooks{})
}
func (s *SignalOutput) start(ctx context.Context) error {
	if err := validateEndpoint(s.endpoint); err != nil {
		return err
	}
	if len(s.recipients) == 0 {
		return errors.New("Signal recipients are required")
	}
	if s.client == nil {
		return errors.New("Signal HTTP client is required")
	}
	done, in := s.Done(), s.Input()
	s.Go(func() {
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
				if s.selected[evt.Type] {
					if err := s.deliver(ctx, evt); err != nil && ctx.Err() == nil {
						s.reportError(err)
					}
				}
			}
		}
	})
	return nil
}
func (s *SignalOutput) Stop() error { return s.Shutdown(plugin.ShutdownHooks{}) }

func validateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/v1/rpc" {
		return errors.New("endpoint must be a signal-cli /api/v1/rpc URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") {
		return nil
	}
	return errors.New("Signal endpoint requires HTTPS except on loopback")
}

func (s *SignalOutput) deliver(ctx context.Context, evt event.Event) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode Signal event: %w", err)
	}
	params := map[string]any{"recipient": s.recipients, "message": string(payload)}
	if s.account != "" {
		params["account"] = s.account
	}
	id := fmt.Sprintf("adder-%d", time.Now().UnixNano())
	requestBody, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "send", "params": params, "id": id})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("Signal RPC request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Signal RPC status %d", resp.StatusCode)
	}
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  *struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode Signal RPC response: %w", err)
	}
	if result.JSONRPC != "2.0" || result.ID != id {
		return errors.New("invalid Signal RPC response")
	}
	if result.Error != nil {
		return fmt.Errorf("Signal RPC error %d: %s", result.Error.Code, result.Error.Message)
	}
	if result.Result == nil || result.Result.Timestamp == 0 {
		return errors.New("Signal RPC send returned no timestamp")
	}
	return nil
}
func (s *SignalOutput) reportError(err error) {
	logger := s.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.signal")
	}
	logger.Error(err.Error())
	s.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*SignalOutput)(nil)
