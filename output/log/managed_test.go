// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package log

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

type failingPayload struct{}

func (failingPayload) MarshalJSON() ([]byte, error) {
	return nil, errors.New("payload-secret-sentinel")
}

func TestConfinedOpenerCompletionAndSanitizedFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	output := New(WithFormat("json"))
	processed := make(chan struct{}, 2)
	output.SetFileOpener(func() (*os.File, error) { return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) })
	output.SetProcessingObserver(func() { processed <- struct{}{} })
	require.NoError(t, output.StartContext(context.Background()))
	t.Cleanup(func() { require.NoError(t, output.Stop()) })
	output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{BlockHash: "good"}}
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("missing completion")
	}
	output.InputChan() <- event.Event{Type: event.TypeRollback, Payload: failingPayload{}}
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatal("missing error completion")
	}
	select {
	case err := <-output.ErrorChan():
		require.ErrorContains(t, err, "serialization failed")
		require.NotContains(t, err.Error(), "payload-secret-sentinel")
	case <-time.After(time.Second):
		t.Fatal("missing sanitized error")
	}
	require.NoError(t, output.Stop())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "good")
	require.NotContains(t, string(data), "payload-secret-sentinel")
}
