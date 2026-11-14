// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

type deadlinePolicyTransport struct {
	sawDeadline bool
}

func (p *deadlinePolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	_, p.sawDeadline = req.Context().Deadline()
	return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestInjectedHTTPPolicyOwnsRequestDeadline(t *testing.T) {
	policy := &deadlinePolicyTransport{}
	p := New()
	p.SetHTTPClient(&http.Client{Transport: policy})
	result := p.sendWebhookData(context.Background(), event.TypeRollback, []byte("{}"), "")
	require.NoError(t, result.err)
	require.False(t, policy.sawDeadline, "an injected policy may wait for quota before applying its request timeout")
}

func TestInstanceHTTPPolicyAndProcessingCompletion(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	p := New(WithUrl(redirect.URL, false), WithRetryConfig(0, time.Millisecond, time.Millisecond))
	p.SetHTTPClient(&http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }})
	done := make(chan struct{}, 2)
	p.SetProcessingObserver(func() { done <- struct{}{} })
	require.NoError(t, p.StartContext(context.Background()))
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	p.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{}}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("missing completion for rejected redirect")
	}
	require.Zero(t, targetCalls.Load())
	p.InputChan() <- event.Event{Type: event.TypeBlock, Payload: event.BlockEvent{}}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("missing completion for invalid context")
	}
	require.Zero(t, targetCalls.Load())
}
