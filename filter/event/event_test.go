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

package event_test

import (
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	filterevent "github.com/blinklabs-io/adder/filter/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventFilterSingleType verifies that configuring a single allowed event type routes matching events and drops all others.
func TestEventFilterSingleType(t *testing.T) {
	e := filterevent.New(
		filterevent.WithTypes([]string{"input.block"}),
	)
	err := e.Start()
	require.NoError(t, err)
	defer func() {
		err := e.Stop()
		require.NoError(t, err)
	}()

	// Send mismatched events followed by a matched event, then verify that
	// only the matched event is received first (proving the mismatched events
	// were dropped sequentially without blocking).
	mismatchedTypes := []string{"input.transaction", "input.rollback", ""}
	for _, typ := range mismatchedTypes {
		e.InputChan() <- event.Event{Type: typ}
		e.InputChan() <- event.Event{Type: "input.block"}

		select {
		case out := <-e.OutputChan():
			assert.Equal(t, "input.block", out.Type, "expected mismatched event to be dropped and matched event delivered first")
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for matched event")
		}
	}
}

// TestEventFilterMultipleTypes verifies that configuring multiple allowed event types routes matching events and drops others.
func TestEventFilterMultipleTypes(t *testing.T) {
	e := filterevent.New(
		filterevent.WithTypes([]string{"input.block", "input.transaction"}),
	)
	err := e.Start()
	require.NoError(t, err)
	defer func() {
		err := e.Stop()
		require.NoError(t, err)
	}()

	// Verify matched types pass through
	matchedTypes := []string{"input.block", "input.transaction"}
	for _, typ := range matchedTypes {
		e.InputChan() <- event.Event{Type: typ}

		select {
		case out := <-e.OutputChan():
			assert.Equal(t, typ, out.Type)
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for matched event of type %s", typ)
		}
	}

	// Verify mismatched types are dropped deterministically by following them with a matched type
	mismatchedTypes := []string{"input.rollback", "input.unknown", ""}
	for _, typ := range mismatchedTypes {
		e.InputChan() <- event.Event{Type: typ}
		e.InputChan() <- event.Event{Type: "input.block"}

		select {
		case out := <-e.OutputChan():
			assert.Equal(t, "input.block", out.Type, "expected mismatched event to be dropped and matched event delivered first")
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for matched event after sending mismatched %s", typ)
		}
	}
}

// TestEventFilterPassAll verifies that an empty allow list allows all event types to pass through unchanged.
func TestEventFilterPassAll(t *testing.T) {
	// Configure with an empty allow list
	e := filterevent.New(
		filterevent.WithTypes([]string{}),
	)
	err := e.Start()
	require.NoError(t, err)
	defer func() {
		err := e.Stop()
		require.NoError(t, err)
	}()

	types := []string{
		"input.block",
		"input.transaction",
		"input.rollback",
		"input.unknown",
		"",
	}
	for _, typ := range types {
		evt := event.Event{Type: typ}
		e.InputChan() <- evt

		select {
		case out := <-e.OutputChan():
			assert.Equal(t, typ, out.Type)
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for event of type %s", typ)
		}
	}
}

// TestEventFilterUnknownType verifies that unrecognized event types are dropped when filtered but pass through if unconfigured.
func TestEventFilterUnknownType(t *testing.T) {
	// 1. With filter types configured, an unknown type should be dropped
	e1 := filterevent.New(
		filterevent.WithTypes([]string{"input.block"}),
	)
	err := e1.Start()
	require.NoError(t, err)
	defer func() {
		err := e1.Stop()
		require.NoError(t, err)
	}()

	e1.InputChan() <- event.Event{Type: "unknown.type"}
	e1.InputChan() <- event.Event{Type: "input.block"}
	select {
	case out := <-e1.OutputChan():
		assert.Equal(t, "input.block", out.Type, "expected unknown type to be dropped and matched event delivered first")
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for matched event after sending unknown type")
	}

	// 2. With no filter types configured (empty list/nil), an unknown type should pass through
	e2 := filterevent.New()
	err = e2.Start()
	require.NoError(t, err)
	defer func() {
		err := e2.Stop()
		require.NoError(t, err)
	}()

	e2.InputChan() <- event.Event{Type: "unknown.type"}
	select {
	case out := <-e2.OutputChan():
		assert.Equal(t, "unknown.type", out.Type)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for unknown.type on unconfigured filter")
	}
}

// TestEventFilterLifecycle verifies that the plugin channels are nil prior to starting and initialized after Start.
func TestEventFilterLifecycle(t *testing.T) {
	e := filterevent.New()
	assert.Nil(t, e.InputChan())
	assert.Nil(t, e.OutputChan())

	err := e.Start()
	require.NoError(t, err)

	assert.NotNil(t, e.InputChan())
	assert.NotNil(t, e.OutputChan())

	err = e.Stop()
	require.NoError(t, err)
}

// TestNewFromCmdlineOptions verifies that the CLI registration factory creates a valid Event filter instance.
func TestNewFromCmdlineOptions(t *testing.T) {
	p := filterevent.NewFromCmdlineOptions()
	assert.NotNil(t, p)
	assert.IsType(t, &filterevent.Event{}, p)
}

// TestEventFilter_ErrorChan verifies that ErrorChan returns nil as the event filter does not produce asynchronous errors.
func TestEventFilter_ErrorChan(t *testing.T) {
	e := filterevent.New()
	assert.Nil(t, e.ErrorChan())
}
