// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSqliteQueuePersistsOrderAndAcknowledgements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := NewSqliteStore(path)
	require.NoError(t, err)
	first, err := store.Enqueue("webhook", []byte("first"), 2)
	require.NoError(t, err)
	second, err := store.Enqueue("webhook", []byte("second"), 2)
	require.NoError(t, err)
	require.Greater(t, second, first)
	_, err = store.Enqueue("webhook", []byte("third"), 2)
	require.ErrorIs(t, err, ErrQueueFull)
	require.NoError(t, store.Close())

	store, err = NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	entry, ok, err := store.Peek("webhook")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first, entry.ID)
	require.Equal(t, []byte("first"), entry.Value)
	require.False(t, entry.CreatedAt.IsZero())
	require.NoError(t, store.Ack("webhook", first))
	entry, ok, err = store.Peek("webhook")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, second, entry.ID)
	require.ErrorContains(t, store.Ack("webhook", first), "not pending")
	require.NoError(t, store.Ack("webhook", second))
	_, ok, err = store.Peek("webhook")
	require.NoError(t, err)
	require.False(t, ok)
	count, err := store.QueueLen("webhook")
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestSqliteQueueNamespacesAreIndependent(t *testing.T) {
	store, err := NewSqliteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	_, err = store.Enqueue("one", []byte("a"), 1)
	require.NoError(t, err)
	_, err = store.Enqueue("two", []byte("b"), 1)
	require.NoError(t, err)
	count, err := store.QueueLen("one")
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = store.QueueLen("two")
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestSqliteQueueWaitsForOtherStoreWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	first, err := NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	tx, err := first.db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO state(key, value) VALUES (?, ?)`, "cursor", []byte("point"))
	require.NoError(t, err)
	appended := make(chan error, 1)
	go func() {
		_, err := second.Enqueue("webhook", []byte("body"), 11)
		appended <- err
	}()
	select {
	case err := <-appended:
		t.Fatalf("queue append returned while another writer held SQLite: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	select {
	case err := <-appended:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("queue append did not resume after writer committed")
	}
}

func TestSqliteQueueBoundAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded.sqlite")
	first, err := NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := NewSqliteStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	results := make(chan error, 2)
	for _, store := range []*SqliteStore{first, second} {
		go func(s *SqliteStore) {
			_, err := s.Enqueue("webhook", []byte("body"), 1)
			results <- err
		}(store)
	}
	got := []error{<-results, <-results}
	if got[0] == nil {
		require.ErrorIs(t, got[1], ErrQueueFull)
	} else {
		require.ErrorIs(t, got[0], ErrQueueFull)
		require.NoError(t, got[1])
	}
	count, err := first.QueueLen("webhook")
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
