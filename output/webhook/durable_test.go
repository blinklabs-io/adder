// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"crypto/sha256"
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
	_, err := secureStateFile(path)
	require.NoError(t, err)
	for _, name := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Stat(name)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestDurableWebhookRejectsMainStateSymlink(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		name := "existing"
		if dangling {
			name = "dangling"
		}
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "target.sqlite")
			var before []byte
			if !dangling {
				store, err := state.NewSqliteStore(target)
				require.NoError(t, err)
				require.NoError(t, store.Close())
				before, err = os.ReadFile(target)
				require.NoError(t, err)
				require.NoError(t, os.Chmod(target, 0o644))
			}
			link := filepath.Join(parent, "webhook.sqlite")
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skipf("symlinks require Windows privileges: %v", err)
				}
				require.NoError(t, err)
			}
			o := New(WithDurableQueue(link, 11))
			require.ErrorContains(t, o.Start(), "not a symlink")
			if dangling {
				_, err := os.Stat(target)
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				data, err := os.ReadFile(target)
				require.NoError(t, err)
				require.Equal(t, before, data)
				if runtime.GOOS != "windows" {
					info, err := os.Stat(target)
					require.NoError(t, err)
					require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
				}
			}
		})
	}
}

func TestDurableWebhookRejectsWritableStateDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions are ACL based")
	}
	parent := filepath.Join(t.TempDir(), "shared")
	require.NoError(t, os.Mkdir(parent, 0o700))
	require.NoError(t, os.Chmod(parent, 0o777))
	o := New(WithDurableQueue(filepath.Join(parent, "webhook.sqlite"), 11))
	require.ErrorContains(t, o.Start(), "writable by another user")
}

func TestDurableWebhookRejectsWritableAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions are ACL based")
	}
	ancestor := filepath.Join(t.TempDir(), "shared")
	require.NoError(t, os.Mkdir(ancestor, 0o700))
	require.NoError(t, os.Chmod(ancestor, 0o777))
	parent := filepath.Join(ancestor, "private")
	require.NoError(t, os.Mkdir(parent, 0o700))
	o := New(WithDurableQueue(filepath.Join(parent, "webhook.sqlite"), 11))
	require.ErrorContains(t, o.Start(), "writable by another user")
}

func TestDurableWebhookRejectsStickyImmediateParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions are ACL based")
	}
	shared, err := filepath.EvalSymlinks("/tmp")
	require.NoError(t, err)
	info, err := os.Stat(shared)
	require.NoError(t, err)
	if info.Mode()&os.ModeSticky == 0 || info.Mode().Perm()&0o022 == 0 {
		t.Skip("/tmp is not a sticky shared directory")
	}
	_, err = validateStateDirectory(shared)
	require.ErrorContains(t, err, "writable by another user")
}

func TestDurableWebhookOpensCanonicalStatePathAfterAliasSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory checks and symlinks")
	}
	root := t.TempDir()
	safe := filepath.Join(root, "safe", "child")
	require.NoError(t, os.MkdirAll(safe, 0o700))
	shared := filepath.Join(root, "shared")
	require.NoError(t, os.Mkdir(shared, 0o700))
	require.NoError(t, os.Chmod(shared, 0o777))
	redirect := filepath.Join(shared, "redirect")
	require.NoError(t, os.Symlink(filepath.Dir(safe), redirect))
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(redirect, alias))
	configured := filepath.Join(alias, "child", "webhook.sqlite")
	realSafe, err := filepath.EvalSymlinks(safe)
	require.NoError(t, err)

	attacker := filepath.Join(root, "attacker", "child")
	require.NoError(t, os.MkdirAll(attacker, 0o700))
	target := filepath.Join(attacker, "webhook.sqlite")
	sentinel, err := state.NewSqliteStore(target)
	require.NoError(t, err)
	require.NoError(t, sentinel.Set("sentinel", []byte("untouched")))
	require.NoError(t, sentinel.Close())
	before, err := os.ReadFile(target)
	require.NoError(t, err)
	o := New(WithDurableQueue(configured, 11))
	var openedPath string
	err = o.openQueueWith(func(opened string) (*state.SqliteStore, error) {
		openedPath = opened
		require.NoError(t, os.Remove(redirect))
		require.NoError(t, os.Symlink(filepath.Dir(attacker), redirect))
		return state.NewSqliteStore(opened)
	})
	require.NoError(t, err)
	require.NoError(t, o.closeQueue())
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(data))
	require.Equal(t, filepath.Join(realSafe, "webhook.sqlite"), openedPath)
}

func TestDurableWebhookRejectsCanonicalPathWithSQLiteQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain question marks")
	}
	root := t.TempDir()
	target := filepath.Join(root, "private?cache=shared")
	require.NoError(t, os.Mkdir(target, 0o700))
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(target, alias))
	configured := filepath.Join(alias, "child", "webhook.sqlite")
	require.NoError(t, os.Mkdir(filepath.Join(target, "child"), 0o700))
	_, err := secureStateFile(configured)
	require.ErrorContains(t, err, "query parameters")
	_, err = os.Stat(filepath.Join(target, "child", "webhook.sqlite"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(root, "private"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDurableWebhookRejectsParentTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "webhook.sqlite"
	_, err := durableFileName(path)
	require.ErrorContains(t, err, "parent-directory components")
	require.ErrorContains(t, New(WithDurableQueue(path, 11)).Start(), "parent-directory components")
}

func TestDurableWebhookRejectsDirectoryStatePath(t *testing.T) {
	parent := t.TempDir()
	path := parent + string(os.PathSeparator)
	require.ErrorContains(t, New(WithDurableQueue(path, 11)).Start(), "file, not a directory")
	_, err := os.Stat(filepath.Join(parent, filepath.Base(parent)))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDurableWebhookRejectsSQLiteDSNParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	for _, suffix := range []string{
		"?cache=shared",
		"?_pragma=synchronous(OFF)",
		"?_pragma=journal_mode(OFF)",
		"?vfs=unknown",
	} {
		t.Run(suffix, func(t *testing.T) {
			name := path + suffix
			o := New(WithDurableQueue(name, 11))
			require.ErrorContains(t, o.Start(), "query parameters")
			_, err := plugin.GetPlugin(plugin.PluginTypeOutput, "webhook", map[string]any{"state-path": name})
			require.ErrorContains(t, err, "query parameters")
			_, err = os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestDurableWebhookRejectsSidecarSymlinkWithoutTouchingTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o644))
	require.NoError(t, os.Chmod(target, 0o644))
	path := filepath.Join(parent, "webhook.sqlite")
	if err := os.Symlink(target, path+"-wal"); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks require Windows privileges: %v", err)
		}
		require.NoError(t, err)
	}
	require.ErrorContains(t, New(WithDurableQueue(path, 11)).Start(), "not a symlink")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, []byte("unchanged"), data)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	}
}

func TestRestrictExistingStateFileDetectsReplacementBeforeOpen(t *testing.T) {
	for _, replacement := range []string{"regular", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "webhook.sqlite-wal")
			target := filepath.Join(parent, "target")
			require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
			require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o644))
			require.NoError(t, os.Chmod(target, 0o644))
			before, err := os.Stat(target)
			require.NoError(t, err)
			initial, err := os.Lstat(path)
			require.NoError(t, err)
			require.NoError(t, os.Rename(path, path+".old"))
			if replacement == "symlink" {
				if err := os.Symlink(target, path); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlinks require Windows privileges: %v", err)
					}
					require.NoError(t, err)
				}
			} else {
				require.NoError(t, os.WriteFile(path, []byte("replacement"), 0o644))
			}
			replacementBefore, err := os.Lstat(path)
			require.NoError(t, err)
			require.ErrorContains(t, restrictOpenedStateFile(path, initial), "changed while opening")
			replacementAfter, err := os.Lstat(path)
			require.NoError(t, err)
			require.Equal(t, replacementBefore.Mode().Perm(), replacementAfter.Mode().Perm())
			if replacement == "regular" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, []byte("replacement"), data)
			}
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, []byte("unchanged"), data)
			after, err := os.Stat(target)
			require.NoError(t, err)
			require.Equal(t, before.Mode().Perm(), after.Mode().Perm())
		})
	}
}

func TestDurableWebhookNewWALSidecarsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix file permissions")
	}
	path := filepath.Join(t.TempDir(), "webhook.sqlite")
	_, err := secureStateFile(path)
	require.NoError(t, err)
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
