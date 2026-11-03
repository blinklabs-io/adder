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
	"sync"
	"testing"

	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/require"
)

// fakeStore is a minimal in-memory state.Store for exercising cursor
// persistence without touching disk. setCalls counts Set invocations so
// tests can assert flushCursor coalesces or skips redundant writes.
type fakeStore struct {
	mu       sync.Mutex
	values   map[string][]byte
	setCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: make(map[string][]byte)}
}

func (f *fakeStore) Get(key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeStore) Set(key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
	f.setCalls++
	return nil
}

func (f *fakeStore) Close() error { return nil }

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
	point := ocommon.Point{Slot: 12345, Hash: []byte{0xde, 0xad, 0xbe, 0xef}}
	c.saveCursor(point)

	c.loadCursor()

	require.False(t, c.intersectTip)
	require.Equal(t, []ocommon.Point{point}, c.intersectPoints)
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

func TestUpdateStatusRecordsPendingCursor(t *testing.T) {
	store := newFakeStore()
	c := New(WithStateStore(store))

	c.updateStatus(100, 1, "aabbcc", 200, "ddeeff")

	c.stateMu.Lock()
	pending := c.pendingCursor
	c.stateMu.Unlock()
	require.NotNil(t, pending)
	require.Equal(t, uint64(100), pending.Slot)
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
