// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

type durableObserverTransport func(*http.Request) (*http.Response, error)

func (f durableObserverTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func durableObserverResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
}

func awaitDurableObserver(t *testing.T, observed <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatalf("missing %s", description)
	}
}

func TestDurableProcessingObserverFollowsCompletedInput(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var attempts atomic.Int64
	o := New(WithDurableQueue(filepath.Join(t.TempDir(), "webhook.sqlite"), 11), WithRetryConfig(1, time.Millisecond, time.Millisecond))
	o.SetHTTPClient(&http.Client{Transport: durableObserverTransport(func(request *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
				return durableObserverResponse(http.StatusInternalServerError), nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		return durableObserverResponse(http.StatusNoContent), nil
	})})
	observed := make(chan struct{}, 3)
	o.SetProcessingObserver(func() { observed <- struct{}{} })
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- rollbackEvent(1)
	awaitDurableObserver(t, entered, "in-flight HTTP attempt")
	require.Empty(t, observed, "processing has not completed while delivery is still in flight")
	once.Do(func() { close(release) })
	awaitDurableObserver(t, observed, "completion after successful retry")
	require.NoError(t, o.Stop())
	require.Equal(t, int64(2), attempts.Load())
	require.Empty(t, observed, "retries must not generate extra processing completions")
	require.Zero(t, pendingCount(t, o.statePath))
}

func TestDurableProcessingObserverIncludesRejectedInput(t *testing.T) {
	for _, test := range []struct {
		name  string
		event event.Event
	}{
		{"nil payload", event.Event{Type: event.TypeBlock}},
		{"wrong payload", event.Event{Type: event.TypeBlock, Payload: event.RollbackEvent{}}},
		{"wrong context", event.Event{Type: event.TypeBlock, Context: "invalid", Payload: event.BlockEvent{}}},
		{"unknown type", event.Event{Type: "input.unknown", Payload: event.RollbackEvent{}}},
		{"formatting failure", event.Event{Type: event.TypeRollback, Context: make(chan struct{}), Payload: event.RollbackEvent{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var observed, attempts atomic.Int64
			o := New(WithDurableQueue(filepath.Join(t.TempDir(), "webhook.sqlite"), 11))
			o.SetProcessingObserver(func() { observed.Add(1) })
			o.SetHTTPClient(&http.Client{Transport: durableObserverTransport(func(*http.Request) (*http.Response, error) {
				attempts.Add(1)
				return durableObserverResponse(http.StatusNoContent), nil
			})})
			require.NoError(t, o.Start())
			t.Cleanup(func() { require.NoError(t, o.Stop()) })
			o.InputChan() <- test.event
			select {
			case err := <-o.ErrorChan():
				require.Error(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("invalid input was not rejected")
			}
			require.NoError(t, o.Stop())
			require.Equal(t, int64(1), observed.Load())
			require.Zero(t, attempts.Load())
			require.Zero(t, pendingCount(t, o.statePath))
		})
	}
}

func TestDurableProcessingObserverPrecedesTerminalShutdownWait(t *testing.T) {
	for _, reason := range []string{"delivery failure", "closed queue", "oversized entry"} {
		t.Run(reason, func(t *testing.T) {
			o := New(WithDurableQueue(filepath.Join(t.TempDir(), "webhook.sqlite"), 11), WithRetryConfig(0, time.Millisecond, time.Millisecond))
			o.SetHTTPClient(&http.Client{Transport: durableObserverTransport(func(*http.Request) (*http.Response, error) {
				return durableObserverResponse(http.StatusInternalServerError), nil
			})})
			observed := make(chan struct{}, 2)
			o.SetProcessingObserver(func() { observed <- struct{}{} })
			require.NoError(t, o.Start())
			t.Cleanup(func() { _ = o.Stop() })
			evt := rollbackEvent(1)
			switch reason {
			case "closed queue":
				require.NoError(t, o.queue.Close())
			case "oversized entry":
				evt.Payload = event.RollbackEvent{BlockHash: strings.Repeat("a", maxQueuedBytes)}
			}
			o.InputChan() <- evt
			awaitDurableObserver(t, o.Failed(), "terminal failure")
			awaitDurableObserver(t, observed, "processing completion before Stop")
			if reason == "delivery failure" {
				require.NoError(t, o.Stop())
				require.Equal(t, 1, pendingCount(t, o.statePath), "processing completion does not acknowledge failed delivery")
			} else {
				require.Error(t, o.Stop())
				require.Zero(t, pendingCount(t, o.statePath))
			}
			require.Empty(t, observed, "a terminal input must be observed exactly once")
		})
	}
}

func TestDurableProcessingObserverCountsDrainedInputWithoutCountingReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	entered := make(chan struct{})
	var attempts, observed atomic.Int64
	first := New(WithDurableQueue(path, 11))
	first.SetProcessingObserver(func() { observed.Add(1) })
	first.SetHTTPClient(&http.Client{Transport: durableObserverTransport(func(request *http.Request) (*http.Response, error) {
		attempts.Add(1)
		close(entered)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	require.NoError(t, first.Start())
	t.Cleanup(func() { require.NoError(t, first.Stop()) })
	first.InputChan() <- rollbackEvent(1)
	awaitDurableObserver(t, entered, "first request")
	for slot := uint64(2); slot <= 4; slot++ {
		first.InputChan() <- rollbackEvent(slot)
	}
	require.NoError(t, first.Stop())
	require.Equal(t, int64(4), observed.Load(), "shutdown must observe every persisted buffered input")
	require.Equal(t, int64(1), attempts.Load())
	require.Equal(t, 4, pendingCount(t, path), "completion cannot remove canceled deliveries from replay")

	second := New(WithDurableQueue(path, 11))
	completed := make(chan struct{}, 6)
	receipts := make(chan uint64, 5)
	second.SetProcessingObserver(func() { completed <- struct{}{} })
	second.SetHTTPClient(&http.Client{Transport: durableObserverTransport(func(request *http.Request) (*http.Response, error) {
		var evt struct {
			Payload event.RollbackEvent `json:"payload"`
		}
		if err := json.NewDecoder(request.Body).Decode(&evt); err != nil {
			return nil, err
		}
		receipts <- evt.Payload.SlotNumber
		return durableObserverResponse(http.StatusNoContent), nil
	})})
	require.NoError(t, second.Start())
	t.Cleanup(func() { require.NoError(t, second.Stop()) })
	require.Empty(t, completed, "startup replay is not new input processing")
	second.InputChan() <- rollbackEvent(5)
	awaitDurableObserver(t, completed, "new input after replay")
	require.NoError(t, second.Stop())
	require.Empty(t, completed)
	require.Len(t, receipts, 5)
	for _, expected := range []uint64{1, 2, 3, 4, 5} {
		require.Equal(t, expected, <-receipts)
	}
	require.Zero(t, pendingCount(t, path))
}
