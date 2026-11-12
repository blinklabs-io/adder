// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/state"
	"github.com/stretchr/testify/require"
)

func rollbackEvent(slot uint64) event.Event {
	return event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: slot})
}

func pendingCount(t *testing.T, path string) int {
	t.Helper()
	store, err := state.NewSqliteStore(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	count, err := store.QueueLen(queueName)
	require.NoError(t, err)
	return count
}

func TestDurableWebhookReplaysFailureAfterRestart(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	type delivery struct{ eventType, id string }
	received := make(chan delivery, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt event.Event
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- delivery{evt.Type, r.Header.Get("X-Adder-Delivery-ID")}
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	newOutput := func() *WebhookOutput {
		return New(WithUrl(server.URL, false), WithDurableQueue(path, 11), WithRetryConfig(0, time.Millisecond, time.Millisecond))
	}
	first := newOutput()
	require.NoError(t, first.Start())
	first.InputChan() <- rollbackEvent(42)
	select {
	case <-first.Failed():
	case <-time.After(2 * time.Second):
		t.Fatal("webhook failure did not stop the durable output")
	}
	require.ErrorContains(t, first.Failure(), "500")
	require.NoError(t, first.Stop())
	require.Equal(t, 1, pendingCount(t, path))
	status.Store(http.StatusOK)
	second := newOutput()
	require.NoError(t, second.Start())
	t.Cleanup(func() { require.NoError(t, second.Stop()) })
	require.Eventually(t, func() bool {
		count, err := second.queue.QueueLen(queueName)
		return err == nil && count == 0
	}, 2*time.Second, time.Millisecond)
	require.NoError(t, second.Stop())
	require.Len(t, received, 2)
	firstRequest := <-received
	secondRequest := <-received
	require.Equal(t, event.TypeRollback, firstRequest.eventType)
	require.Equal(t, event.TypeRollback, secondRequest.eventType)
	require.Regexp(t, `^[0-9a-f]{32}-[0-9]+$`, firstRequest.id)
	require.Equal(t, firstRequest.id, secondRequest.id)
}

func TestDurableWebhookStopPersistsBufferedEvents(t *testing.T) {
	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(unblock) })
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	o := New(WithUrl(server.URL, false), WithDurableQueue(path, 11))
	require.NoError(t, o.Start())
	o.InputChan() <- rollbackEvent(1)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	for slot := uint64(2); slot <= 11; slot++ {
		o.InputChan() <- rollbackEvent(slot)
	}
	require.NoError(t, o.Stop())
	require.Equal(t, 11, pendingCount(t, path))
}

func TestDurableWebhookRejectsDestinationChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	first := New(WithUrl("https://one.example.org/hook", false), WithDurableQueue(path, 11))
	require.NoError(t, first.Start())
	require.NoError(t, first.Stop())
	other := New(WithUrl("https://two.example.org/hook", false), WithDurableQueue(path, 11))
	require.ErrorContains(t, other.Start(), "different URL or format")
}

func TestDurableWebhookReplaysBlockBeforeRollback(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	received := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt event.Event
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- evt.Type
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	first := New(WithUrl(server.URL, false), WithDurableQueue(path, 11), WithRetryConfig(0, time.Millisecond, time.Millisecond))
	require.NoError(t, first.Start())
	first.InputChan() <- event.New(event.TypeBlock, time.Now(), event.BlockContext{}, event.BlockEvent{})
	select {
	case <-first.Failed():
	case <-time.After(2 * time.Second):
		t.Fatal("block delivery did not fail")
	}
	require.NoError(t, first.Stop())
	store, err := state.NewSqliteStore(path)
	require.NoError(t, err)
	rollback := rollbackEvent(42)
	body, err := formatWebhook(&rollback, "adder")
	require.NoError(t, err)
	value, err := json.Marshal(queuedWebhook{Type: rollback.Type, Body: body})
	require.NoError(t, err)
	_, err = store.Enqueue(queueName, value, 11)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	status.Store(http.StatusOK)
	second := New(WithUrl(server.URL, false), WithDurableQueue(path, 11))
	require.NoError(t, second.Start())
	t.Cleanup(func() { require.NoError(t, second.Stop()) })
	require.Eventually(t, func() bool {
		count, err := second.queue.QueueLen(queueName)
		return err == nil && count == 0
	}, 2*time.Second, time.Millisecond)
	require.NoError(t, second.Stop())
	require.Len(t, received, 3)
	require.Equal(t, event.TypeBlock, <-received)
	require.Equal(t, event.TypeBlock, <-received)
	require.Equal(t, event.TypeRollback, <-received)
}

func TestDurableWebhookRequiresRoomToDrainInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	o := New(WithDurableQueue(path, 10))
	require.ErrorContains(t, o.Start(), "max-pending")
	_, err := plugin.GetPlugin(plugin.PluginTypeOutput, "webhook", map[string]any{"state-path": path, "max-pending": 10})
	require.ErrorContains(t, err, "max-pending")
	for _, path := range []string{":memory:", ":memory:?cache=shared", "file::memory:?cache=shared"} {
		memory := New(WithDurableQueue(path, 11))
		require.ErrorContains(t, memory.Start(), "persistent SQLite file")
		_, err = plugin.GetPlugin(plugin.PluginTypeOutput, "webhook", map[string]any{"state-path": path})
		require.ErrorContains(t, err, "persistent SQLite file")
	}
}

func TestDurableWebhookRestrictsStateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix file permissions")
	}
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	for _, name := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		require.NoError(t, os.WriteFile(name, nil, 0o644))
		require.NoError(t, os.Chmod(name, 0o644))
	}
	require.NoError(t, secureStateFile(path))
	for _, name := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Stat(name)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestDurableWebhookQueryPathUsesPrivateDiskFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	o := New(WithDurableQueue(path+"?cache=shared", 11))
	require.NoError(t, o.Start())
	require.NoError(t, o.Stop())
	_, err := os.Stat(path)
	require.NoError(t, err)
	_, err = os.Stat(path + "?cache=shared")
	require.ErrorIs(t, err, os.ErrNotExist)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestDurableWebhookNewWALSidecarsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix file permissions")
	}
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	require.NoError(t, secureStateFile(path))
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE test (value TEXT); INSERT INTO test(value) VALUES ('body')")
	require.NoError(t, err)
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestDurableWebhookFullQueueReplaysBeforeAcceptingInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	newOutput := func() *WebhookOutput {
		return New(WithUrl(server.URL, false), WithDurableQueue(path, 11), WithRetryConfig(0, time.Millisecond, time.Millisecond))
	}
	first := newOutput()
	require.NoError(t, first.Start())
	require.NoError(t, first.Stop())
	store, err := state.NewSqliteStore(path)
	require.NoError(t, err)
	rollback := rollbackEvent(42)
	body, err := formatWebhook(&rollback, "adder")
	require.NoError(t, err)
	value, err := json.Marshal(queuedWebhook{Type: rollback.Type, Body: body})
	require.NoError(t, err)
	for range 11 {
		_, err := store.Enqueue(queueName, value, 11)
		require.NoError(t, err)
	}
	require.NoError(t, store.Close())
	second := newOutput()
	require.ErrorContains(t, second.Start(), "replay pending webhooks")
	require.Nil(t, second.InputChan())
	require.Equal(t, 11, pendingCount(t, path))
}

func TestDurableWebhookRejectsOversizedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	o := New(WithDurableQueue(path, 11))
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), event.BlockContext{}, event.BlockEvent{BlockHash: strings.Repeat("a", maxQueuedBytes)})
	select {
	case <-o.Failed():
	case <-time.After(2 * time.Second):
		t.Fatal("oversized entry was not rejected")
	}
	require.ErrorContains(t, o.Failure(), "durable queue limit")
	require.ErrorContains(t, o.Stop(), "durable queue limit")
	require.Zero(t, pendingCount(t, path))
}

func TestDurableWebhookDoesNotAckBodylessRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			targetCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	o := New(WithUrl(server.URL, false), WithDurableQueue(path, 11), WithRetryConfig(0, time.Millisecond, time.Millisecond))
	require.NoError(t, o.Start())
	o.InputChan() <- rollbackEvent(42)
	select {
	case <-o.Failed():
	case <-time.After(2 * time.Second):
		t.Fatal("bodyless redirect was not rejected")
	}
	require.ErrorIs(t, o.Failure(), errRedirectDropsBody)
	require.NoError(t, o.Stop())
	require.Zero(t, targetCalls.Load())
	require.Equal(t, 1, pendingCount(t, path))
}
