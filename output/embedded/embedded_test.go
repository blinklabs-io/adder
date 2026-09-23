// Copyright 2026 Blink Labs Software
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

package embedded

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallbackReceivesEvent(t *testing.T) {
	got := make(chan event.Event, 1)
	e := New(WithCallbackFunc(func(evt event.Event) error {
		got <- evt
		return nil
	}))
	require.NoError(t, e.Start())
	defer func() { require.NoError(t, e.Stop()) }()

	e.InputChan() <- event.Event{Type: "chainsync.block"}
	select {
	case evt := <-got:
		assert.Equal(t, "chainsync.block", evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("callback was never invoked")
	}
}

func TestCallbackErrorIsReportedOnErrorChan(t *testing.T) {
	e := New(WithCallbackFunc(func(event.Event) error {
		return errors.New("boom")
	}))
	require.NoError(t, e.Start())
	defer func() { _ = e.Stop() }()

	e.InputChan() <- event.Event{Type: "chainsync.block"}
	select {
	case err := <-e.ErrorChan():
		assert.ErrorContains(t, err, "callback function error: boom")
	case <-time.After(2 * time.Second):
		t.Fatal("callback error was never reported")
	}
}

func TestEventsAreForwardedToTheSuppliedChannel(t *testing.T) {
	forward := make(chan event.Event, 1)
	e := New(WithOutputChan(forward))
	require.NoError(t, e.Start())
	defer func() { require.NoError(t, e.Stop()) }()

	e.InputChan() <- event.Event{Type: "chainsync.transaction"}
	select {
	case evt := <-forward:
		assert.Equal(t, "chainsync.transaction", evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("event was not forwarded to the supplied channel")
	}
}

func TestOutputChanIsAlwaysNil(t *testing.T) {
	e := New(WithOutputChan(make(chan event.Event, 1)))
	require.NoError(t, e.Start())
	defer func() { require.NoError(t, e.Stop()) }()
	assert.Nil(t, e.OutputChan(),
		"the user-supplied chan is not the pipeline output chan")
}

func TestSuppliedChannelIsClosedByStop(t *testing.T) {
	// The plugin is the only sender, so a caller ranging over the channel
	// relies on Stop to end the range.
	forward := make(chan event.Event, 1)
	e := New(WithOutputChan(forward))
	require.NoError(t, e.Start())
	require.NoError(t, e.Stop())

	_, open := <-forward
	assert.False(t, open, "Stop should have closed the supplied channel")
}

func TestRestartWithASuppliedChannelIsRefused(t *testing.T) {
	// Stop closed the caller's channel, so there is nowhere left to
	// forward to. Start must say so rather than come back up dropping
	// every event on the floor.
	e := New(WithOutputChan(make(chan event.Event, 1)))
	require.NoError(t, e.Start())
	require.NoError(t, e.Stop())

	assert.ErrorIs(t, e.Start(), ErrForwardChanClosed)
}

func TestRestartDuringStopRejectsClosedForwardChannel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	e := New(
		WithOutputChan(make(chan event.Event, 1)),
		WithCallbackFunc(func(event.Event) error {
			close(entered)
			<-release
			return nil
		}),
	)
	require.NoError(t, e.Start())
	e.InputChan() <- event.Event{Type: "block"}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("callback did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- e.Stop() }()
	select {
	case <-e.Done():
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Stop did not signal the worker")
	}

	// Keep Stop waiting on the callback while the new Start attempts
	// to capture the forwarding channel that Stop is about to close.
	restarted := make(chan error, 1)
	go func() { restarted <- e.Start() }()
	select {
	case <-restarted:
		close(release)
		t.Fatal("Start returned before Stop finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after callback completion")
	}
	select {
	case err := <-restarted:
		require.ErrorIs(t, err, ErrForwardChanClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop finished")
	}
	assert.False(t, e.Running())
	require.NoError(t, e.Stop())
}

func TestRestartWithoutASuppliedChannelIsAllowed(t *testing.T) {
	// Nothing was closed on the caller's behalf, so the plugin restarts.
	got := make(chan event.Event, 1)
	e := New(WithCallbackFunc(func(evt event.Event) error {
		got <- evt
		return nil
	}))
	require.NoError(t, e.Start())
	require.NoError(t, e.Stop())
	require.NoError(t, e.Start())
	defer func() { require.NoError(t, e.Stop()) }()

	e.InputChan() <- event.Event{Type: "chainsync.block"}
	select {
	case evt := <-got:
		assert.Equal(t, "chainsync.block", evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("callback was never invoked after restart")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	e := New()
	require.NoError(t, e.Start())
	require.NoError(t, e.Stop())
	assert.NotPanics(t, func() { _ = e.Stop() })
}

func TestContextCallbackIsCanceledAndJoinedOnStop(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	e := New(
		WithContextCallbackFunc(func(ctx context.Context, _ event.Event) error {
			close(entered)
			defer close(exited)
			<-ctx.Done()
			return ctx.Err()
		}),
	)
	require.NoError(t, e.Start())
	e.InputChan() <- event.Event{Type: "test"}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback not entered")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- e.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel callback")
	}
	select {
	case <-exited:
	default:
		t.Fatal("Stop did not join callback")
	}
	require.NoError(t, e.Failure(), "normal cancellation must not be terminal")
}
