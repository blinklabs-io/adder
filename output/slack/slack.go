// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const maxRetryAfter = time.Minute

var eventNames = map[string]string{
	"blocks":            event.TypeBlock,
	"transactions":      event.TypeTransaction,
	"rollbacks":         event.TypeRollback,
	"governance":        event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// SlackOutput delivers selected Adder events to Slack incoming webhooks.
type SlackOutput struct {
	plugin.Base
	urls        []string
	selected    map[string]bool
	client      *http.Client
	maxRetries  int
	nextAllowed map[string]time.Time
}

// New builds a Slack output. Plugin configuration validates URLs and event names.
func New(options ...Option) *SlackOutput {
	s := &SlackOutput{
		selected: map[string]bool{
			event.TypeBlock: true, event.TypeTransaction: true,
			event.TypeRollback: true, event.TypeGovernance: true,
		},
		client: &http.Client{Timeout: 15 * time.Second}, maxRetries: 3,
		nextAllowed: make(map[string]time.Time),
	}
	for _, option := range options {
		option(s)
	}
	return s
}

func selectedTypes(names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, errors.New("event-types must name at least one supported type")
	}
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Slack event type %q", name)
		}
		selected[typ] = true
	}
	return selected, nil
}

// Role identifies this plugin as a pipeline output.
func (s *SlackOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// Start begins delivery using a background worker.
func (s *SlackOutput) Start() error { return s.StartContext(context.Background()) }

// StartContext starts the output under ctx.
func (s *SlackOutput) StartContext(ctx context.Context) error {
	return s.StartRun(ctx, plugin.BaseConfig{HasInput: true}, s.start, plugin.ShutdownHooks{})
}

func (s *SlackOutput) start(ctx context.Context) error {
	if len(s.urls) == 0 {
		return errors.New("at least one Slack webhook URL is required")
	}
	for i, destination := range s.urls {
		if err := validateWebhookURL(destination); err != nil {
			return fmt.Errorf("slack destination %d: %w", i+1, err)
		}
	}
	if s.client == nil {
		return errors.New("slack HTTP client is required")
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
				s.deliverEvent(ctx, evt)
			}
		}
	})
	return nil
}

func (s *SlackOutput) deliverEvent(ctx context.Context, evt event.Event) {
	if !s.selected[evt.Type] {
		return
	}
	message, err := formatEvent(evt)
	if err != nil {
		s.reportError(err)
		return
	}
	for i, destination := range s.urls {
		if err := s.deliver(ctx, destination, message); err != nil && ctx.Err() == nil {
			s.reportError(fmt.Errorf("slack destination %d: %w", i+1, err))
		}
	}
}

func formatEvent(evt event.Event) (string, error) {
	switch evt.Type {
	case event.TypeBlock:
		payload, ok := evt.Payload.(event.BlockEvent)
		ctx, ctxOK := evt.Context.(event.BlockContext)
		if !ok || !ctxOK {
			return "", errors.New("invalid block event for Slack output")
		}
		return fmt.Sprintf("Cardano block #%d (slot %d, %s)\nHash: %s\nTransactions: %d",
			ctx.BlockNumber, ctx.SlotNumber, ctx.Era, payload.BlockHash, payload.TransactionCount), nil
	case event.TypeTransaction:
		payload, ok := evt.Payload.(event.TransactionEvent)
		ctx, ctxOK := evt.Context.(event.TransactionContext)
		if !ok || !ctxOK {
			return "", errors.New("invalid transaction event for Slack output")
		}
		return fmt.Sprintf("Cardano transaction %s (block %d, slot %d)\nFee: %d lovelace",
			ctx.TransactionHash, ctx.BlockNumber, ctx.SlotNumber, payload.Fee), nil
	case event.TypeRollback:
		payload, ok := evt.Payload.(event.RollbackEvent)
		if !ok {
			return "", errors.New("invalid rollback event for Slack output")
		}
		return fmt.Sprintf("Cardano rollback to slot %d\nHash: %s", payload.SlotNumber, payload.BlockHash), nil
	case event.TypeGovernance:
		payload, ok := evt.Payload.(event.GovernanceEvent)
		ctx, ctxOK := evt.Context.(event.GovernanceContext)
		if !ok || !ctxOK {
			return "", errors.New("invalid governance event for Slack output")
		}
		return fmt.Sprintf("Cardano governance event in transaction %s (slot %d)\nProposals: %d; votes: %d; DRep certificates: %d",
			ctx.TransactionHash, ctx.SlotNumber, len(payload.ProposalProcedures), len(payload.VotingProcedures), len(payload.DRepCertificates)), nil
	case event.TypeDRepRegistration, event.TypeDRepUpdate, event.TypeDRepRetirement:
		payload, ok := evt.Payload.(event.DRepCertificateEvent)
		ctx, ctxOK := evt.Context.(event.GovernanceContext)
		if !ok || !ctxOK {
			return "", errors.New("invalid DRep event for Slack output")
		}
		return fmt.Sprintf("Cardano DRep %s: %s (transaction %s, slot %d)",
			payload.Certificate.CertificateType, payload.Certificate.DRepId,
			ctx.TransactionHash, ctx.SlotNumber), nil
	default:
		return "", fmt.Errorf("unsupported Slack event type %q", evt.Type)
	}
}

type response struct {
	status     int
	retryAfter string
	body       string
}

func (s *SlackOutput) post(ctx context.Context, destination, message string) (response, error) {
	data, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: message})
	if err != nil {
		return response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(data))
	if err != nil {
		return response{}, errors.New("invalid Slack webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return response{}, urlErr.Err
		}
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: strings.TrimSpace(string(body))}, nil
}

func (s *SlackOutput) deliver(ctx context.Context, destination, message string) error {
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		if remaining := time.Until(s.nextAllowed[destination]); remaining > 0 {
			if remaining > maxRetryAfter {
				return errors.New("slack destination is cooling down")
			}
			if err := wait(ctx, remaining); err != nil {
				return err
			}
		}
		resp, err := s.post(ctx, destination, message)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt == s.maxRetries {
				return fmt.Errorf("request failed after %d attempts: %w", attempt+1, err)
			}
			if err := wait(ctx, time.Second<<attempt); err != nil {
				return err
			}
			continue
		}
		if resp.status == http.StatusOK && resp.body == "ok" {
			s.nextAllowed[destination] = time.Now().Add(time.Second)
			return nil
		}
		if resp.status == http.StatusTooManyRequests {
			seconds, err := strconv.ParseInt(resp.retryAfter, 10, 64)
			if err != nil || seconds < 0 || seconds > int64((1<<63-1)/time.Second) {
				s.nextAllowed[destination] = time.Now().Add(maxRetryAfter)
				return errors.New("slack rate limit has invalid Retry-After")
			}
			delay := time.Duration(seconds) * time.Second
			s.nextAllowed[destination] = time.Now().Add(delay)
			if delay > maxRetryAfter {
				return errors.New("slack rate limit exceeds retry window")
			}
			if attempt == s.maxRetries {
				return errors.New("slack rate limit retry budget exhausted")
			}
			continue
		}
		if resp.status >= 500 && attempt < s.maxRetries {
			if err := wait(ctx, time.Second<<attempt); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("slack webhook returned HTTP %d", resp.status)
	}
	return errors.New("slack delivery retry budget exhausted")
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

func (s *SlackOutput) reportError(err error) {
	logger := s.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.slack")
	}
	logger.Error(err.Error())
	s.TrySendError(err)
}

// Stop waits for the delivery worker to exit.
func (s *SlackOutput) Stop() error { return s.Shutdown(plugin.ShutdownHooks{}) }

var _ plugin.ManagedPlugin = (*SlackOutput)(nil)
