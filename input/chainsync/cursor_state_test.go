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

package chainsync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/state"
	protocolchainsync "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/require"
)

// fakeStore is a minimal in-memory state.Store for exercising cursor
// persistence without touching disk. setCalls counts Set invocations so
// tests can assert flushCursor coalesces or skips redundant writes.
type fakeStore struct {
	mu         sync.Mutex
	values     map[string][]byte
	setCalls   int
	getErr     error
	setErr     error
	closeCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: make(map[string][]byte)}
}

func (f *fakeStore) Get(key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeStore) Set(key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = value
	f.setCalls++
	return nil
}

func (f *fakeStore) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

func TestLoadCursorWithoutStoreIsNoop(t *testing.T) {
	c := New()
	c.intersectTip = true
	c.loadCursor()
	require.True(t, c.intersectTip)
	require.Empty(t, c.intersectPoints)
}

func TestLoadCursorWithNoSavedCursorKeepsConfiguredIntersect(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store), WithIntersectTip(true))
	c.loadCursor()
	require.True(t, c.intersectTip)
	require.Empty(t, c.intersectPoints)
}

func TestLoadCursorWithSavedCursorOverridesIntersectTip(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store), WithIntersectTip(true))
	point := ocommon.Point{Slot: 12345, Hash: make([]byte, 32)}
	point.Hash[0] = 0xde
	point.Hash[1] = 0xad
	c.saveCursor(point)

	c.loadCursor()

	require.False(t, c.intersectTip)
	require.Equal(t, []ocommon.Point{point}, c.intersectPoints)
	require.Equal(t, []ocommon.Point{point}, c.emittedCursors)
}

func TestLoadedCursorSurvivesRollbackBeforeNewBlock(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store))
	saved := ocommon.Point{Slot: 12345, Hash: make([]byte, 32)}
	saved.Hash[0] = 0xde
	c.saveCursor(saved)
	require.NoError(t, c.loadCursor())

	rollback := ocommon.Point{Slot: 13000, Hash: make([]byte, 32)}
	require.NoError(t, c.handleRollBackward(
		protocolchainsync.CallbackContext{},
		rollback,
		protocolchainsync.Tip{},
	))

	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	require.Equal(t, []ocommon.Point{saved}, c.emittedCursors)
	require.NotNil(t, c.pendingCursor)
	require.Equal(t, saved, *c.pendingCursor)
}

func TestLoadCursorRejectsInvalidSavedHash(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store), WithIntersectTip(true))
	store.values[defaultStateKey] = []byte(`{"slot":12345,"hash":"deadbeef"}`)

	err := c.loadCursor()

	require.Error(t, err)
	require.True(t, c.intersectTip)
	require.Empty(t, c.intersectPoints)
}

func TestLoadCursorReturnsStoreReadError(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("temporary read failure")
	c := New(WithStateStore(store))

	require.Error(t, c.loadCursor())
}

func TestFlushCursorPersistsOnlyWhenPending(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store))

	// No pending cursor yet.
	c.flushCursor()
	require.Equal(t, 0, store.setCalls)

	point := ocommon.Point{Slot: 42, Hash: []byte{0x01}}
	c.stateMu.Lock()
	c.pendingCursor = &point
	c.stateMu.Unlock()

	c.flushCursor()
	require.Equal(t, 1, store.setCalls)

	// Already flushed; nothing new to persist.
	c.flushCursor()
	require.Equal(t, 1, store.setCalls)
}

func TestFlushCursorRetainsPendingCursorAfterWriteFailure(t *testing.T) {
	store := newFakeStore()
	store.setErr = errors.New("temporary write failure")
	c := New(WithStateStore(store))
	point := ocommon.Point{Slot: 42, Hash: make([]byte, 32)}
	c.setPendingCursor(point)

	require.Error(t, c.flushCursor())
	c.stateMu.Lock()
	require.NotNil(t, c.pendingCursor)
	c.stateMu.Unlock()

	store.setErr = nil
	require.NoError(t, c.flushCursor())
	c.stateMu.Lock()
	require.Nil(t, c.pendingCursor)
	c.stateMu.Unlock()
}

func TestUpdateStatusDoesNotRecordUnemittedCursor(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store))

	c.updateStatus(100, 1, "aabbcc", 200, "ddeeff")

	c.stateMu.Lock()
	pending := c.pendingCursor
	c.stateMu.Unlock()
	require.Nil(t, pending)
}

func TestRollbackWithDelayedEventsKeepsLastEmittedCursor(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store), WithDelayConfirmations(2))
	emitted := ocommon.Point{Slot: 100, Hash: make([]byte, 32)}
	c.setPendingCursor(emitted)
	c.delayBuffer = [][]event.Event{{
		event.New(
			event.TypeBlock,
			time.Now(),
			event.BlockContext{SlotNumber: 200},
			nil,
		),
	}}

	rollback := ocommon.Point{Slot: 150, Hash: make([]byte, 32)}
	require.NoError(t, c.handleRollBackward(
		protocolchainsync.CallbackContext{},
		rollback,
		protocolchainsync.Tip{},
	))

	c.stateMu.Lock()
	pending := c.pendingCursor
	c.stateMu.Unlock()
	require.NotNil(t, pending)
	require.Equal(t, emitted, *pending)
}

func TestRollbackWithRetainedDelayedEventKeepsLastEmittedCursor(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store), WithDelayConfirmations(2))
	emitted := ocommon.Point{Slot: 100, Hash: make([]byte, 32)}
	c.setPendingCursor(emitted)
	c.delayBuffer = [][]event.Event{{
		event.New(
			event.TypeBlock,
			time.Now(),
			event.BlockContext{SlotNumber: 140},
			nil,
		),
	}}

	rollback := ocommon.Point{Slot: 150, Hash: make([]byte, 32)}
	require.NoError(t, c.handleRollBackward(
		protocolchainsync.CallbackContext{},
		rollback,
		protocolchainsync.Tip{},
	))

	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	require.Equal(t, emitted, *c.pendingCursor)
}

func TestWithStateKeyOverridesDefault(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store), WithStateKey("custom-key"))
	point := ocommon.Point{Slot: 1, Hash: []byte{0x01}}
	c.saveCursor(point)

	_, ok, err := store.Get("custom-key")
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = store.Get(defaultStateKey)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestOwnedStateStoreReopensAfterStop(t *testing.T) {
	initial := newFakeStore()
	opened := newFakeStore()
	openCalls := 0
	c := New(withOwnedStateStore(initial, func() (state.Store, error) {
		openCalls++
		return opened, nil
	}))

	setup := func(context.Context) error { return c.ensureStateStore() }
	require.NoError(t, c.StartRun(context.Background(), chainSyncBaseConfig(), setup, c.shutdownHooks()))
	require.NoError(t, c.Stop())
	require.Equal(t, 1, initial.closeCalls)

	require.NoError(t, c.StartRun(context.Background(), chainSyncBaseConfig(), setup, c.shutdownHooks()))
	require.Equal(t, opened, c.stateStore)
	require.Equal(t, 1, openCalls)
	require.NoError(t, c.Stop())
	require.Equal(t, 1, opened.closeCalls)
}

func TestCallerStateStoreRemainsOpenAfterStop(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store))
	setup := func(context.Context) error { return c.ensureStateStore() }

	require.NoError(t, c.StartRun(context.Background(), chainSyncBaseConfig(), setup, c.shutdownHooks()))
	require.NoError(t, c.Stop())
	require.Equal(t, 0, store.closeCalls)
}
