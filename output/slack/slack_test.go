// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func slackResponse(status int, body, retryAfter string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Retry-After": []string{retryAfter}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func rollbackEvent() event.Event {
	return event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{SlotNumber: 42, BlockHash: "abc"}}
}

func TestSlackWebhookPayloadAndDestinations(t *testing.T) {
	var destinations []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		var payload struct {
			Text string `json:"text"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		require.Contains(t, payload.Text, "Cardano rollback to slot 42")
		destinations = append(destinations, req.URL.Path)
		return slackResponse(http.StatusOK, "ok", ""), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/one", "https://hooks.slack.com/services/two"), WithHTTPClient(client))
	s.deliverEvent(context.Background(), rollbackEvent())
	require.Equal(t, []string{"/services/one", "/services/two"}, destinations)
}

func TestSlackRateLimitUsesRetryAfter(t *testing.T) {
	var attempts int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return slackResponse(http.StatusTooManyRequests, "rate_limited", "0"), nil
		}
		return slackResponse(http.StatusOK, "ok", ""), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/test"), WithHTTPClient(client))
	require.NoError(t, s.deliver(context.Background(), s.urls[0], "test"))
	require.Equal(t, 2, attempts)
}

func TestSlackRateLimitWithoutUsableRetryAfterFails(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return slackResponse(http.StatusTooManyRequests, "rate_limited", ""), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/test"), WithHTTPClient(client))
	require.ErrorContains(t, s.deliver(context.Background(), s.urls[0], "test"), "Retry-After")
}

func TestSlackCooldownSurvivesExhaustedRetryBudget(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return slackResponse(http.StatusTooManyRequests, "rate_limited", "1"), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/test"), WithHTTPClient(client), WithMaxRetries(0))
	require.ErrorContains(t, s.deliver(context.Background(), s.urls[0], "first"), "retry budget exhausted")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.deliver(ctx, s.urls[0], "second"), context.DeadlineExceeded)
	require.EqualValues(t, 1, attempts.Load(), "second event must not be posted during cooldown")
}

func TestSlackLongCooldownSkipsLaterEvents(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return slackResponse(http.StatusTooManyRequests, "rate_limited", "61"), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/test"), WithHTTPClient(client))
	require.ErrorContains(t, s.deliver(context.Background(), s.urls[0], "first"), "exceeds retry window")
	require.ErrorContains(t, s.deliver(context.Background(), s.urls[0], "second"), "cooling down")
	require.EqualValues(t, 1, attempts.Load(), "cooling destination must not receive another request")
}

func TestSlackMalformedURLDoesNotExposeSecret(t *testing.T) {
	s := New(WithWebhookURLs("https://hooks.slack.com/services/secret%zz"), WithMaxRetries(0))
	err := s.Start()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
	err = s.deliver(context.Background(), s.urls[0], "test")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}

func TestSlackDeliveryErrorIsVisibleWithoutWebhookSecret(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return slackResponse(http.StatusBadRequest, "invalid_payload", ""), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/secret"), WithHTTPClient(client), WithMaxRetries(0))
	require.NoError(t, s.Start())
	t.Cleanup(func() { require.NoError(t, s.Stop()) })
	s.InputChan() <- rollbackEvent()
	select {
	case err := <-s.ErrorChan():
		require.ErrorContains(t, err, "HTTP 400")
		require.NotContains(t, err.Error(), "secret")
	case <-time.After(2 * time.Second):
		t.Fatal("Slack delivery error was not surfaced")
	}
}

func TestSlackEventSelectionSkipsUnselectedEvents(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return slackResponse(http.StatusOK, "ok", ""), nil
	})}
	s := New(WithWebhookURLs("https://hooks.slack.com/services/test"), WithHTTPClient(client),
		WithEventTypes(map[string]bool{event.TypeBlock: true}))
	s.deliverEvent(context.Background(), rollbackEvent())
	require.Zero(t, calls.Load())
	s.deliverEvent(context.Background(), event.Event{Type: event.TypeBlock,
		Context: event.BlockContext{BlockNumber: 1}, Payload: event.BlockEvent{BlockHash: "abc"}})
	require.EqualValues(t, 1, calls.Load())
}

func TestSlackFactoryValidatesConfig(t *testing.T) {
	for _, tc := range []struct {
		urls, eventTypes string
		wantError        string
	}{
		{"", "blocks", "webhook-urls"},
		{"not-a-url", "blocks", "webhook-urls"},
		{"https://hooks.slack.com/services/test", "unknown", "unsupported Slack event type"},
		{"https://hooks.slack.com/services/test", "blocks, rollbacks", ""},
	} {
		cfg, err := plugin.ResolveConfig(map[string]map[string]map[string]any{
			"output": {"slack": {"webhook-urls": tc.urls, "event-types": tc.eventTypes}},
		}, nil, func(string) (string, bool) { return "", false })
		require.NoError(t, err)
		options, err := cfg.Options(plugin.PluginTypeOutput, "slack")
		require.NoError(t, err)
		_, err = newFromOptions(options)
		if tc.wantError == "" {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, tc.wantError)
		}
	}
}

func TestSlackFormatsSupportedEvents(t *testing.T) {
	for _, tc := range []struct {
		evt  event.Event
		want string
	}{
		{event.Event{Type: event.TypeBlock, Context: event.BlockContext{BlockNumber: 7, SlotNumber: 42, Era: "Conway"}, Payload: event.BlockEvent{BlockHash: "abc", TransactionCount: 2}}, "Cardano block #7"},
		{event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{TransactionHash: "txhash", SlotNumber: 42}, Payload: event.TransactionEvent{Fee: 123}}, "Fee: 123 lovelace"},
		{rollbackEvent(), "rollback to slot 42"},
		{event.Event{Type: event.TypeGovernance, Context: event.GovernanceContext{TransactionHash: "govhash", SlotNumber: 42}, Payload: event.GovernanceEvent{}}, "governance event in transaction govhash"},
		{event.Event{Type: event.TypeDRepRegistration, Context: event.GovernanceContext{TransactionHash: "govhash", SlotNumber: 42}, Payload: event.DRepCertificateEvent{Certificate: event.DRepCertificateData{CertificateType: "Registration", DRepId: "drep1"}}}, "DRep Registration: drep1"},
	} {
		text, err := formatEvent(tc.evt)
		require.NoError(t, err)
		require.Contains(t, text, tc.want)
	}
}

func TestSlackLifecycleContract(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return slackResponse(http.StatusOK, "ok", ""), nil
	})}
	plugintest.Lifecycle(t, New(WithWebhookURLs("https://hooks.slack.com/services/test"), WithHTTPClient(client)))
}

func TestSlackFailedStartContract(t *testing.T) {
	plugintest.FailedStart(t, New())
}
