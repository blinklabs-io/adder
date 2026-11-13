// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/state"
)

const (
	queueName      = "webhook.pending"
	destinationKey = "webhook.destination"
	queueIDKey     = "webhook.queue-id"
	maxQueuedBytes = 16 << 20
)

type queuedWebhook struct {
	Type string `json:"type"`
	Body []byte `json:"body"`
}

func durableFileName(path string) (string, error) {
	name, _, _ := strings.Cut(path, "?")
	if name == ":memory:" || strings.HasPrefix(path, "file:") {
		return "", errors.New("state-path must name a persistent SQLite file")
	}
	return name, nil
}

func secureStateFile(name string) error {
	if runtime.GOOS != "windows" {
		parent, err := os.Lstat(filepath.Dir(name))
		if err != nil {
			return fmt.Errorf("inspect state-path directory: %w", err)
		}
		if !parent.IsDir() || parent.Mode().Perm()&0o022 != 0 {
			return errors.New("state-path directory must not be a symlink or writable by group or others")
		}
	}
	// Exclusive creation never follows an existing symlink. For an existing
	// database, inspect the path before opening and verify the opened file is
	// still the same regular file.
	file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(name)
		if statErr != nil {
			return fmt.Errorf("inspect webhook state file: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return errors.New("state-path must name a regular file, not a symlink")
		}
		file, err = os.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			opened, openErr := file.Stat()
			current, pathErr := os.Lstat(name)
			if openErr != nil || pathErr != nil || !os.SameFile(opened, current) {
				_ = file.Close()
				return errors.New("state-path changed while opening the SQLite file")
			}
		}
	}
	if err != nil {
		return fmt.Errorf("open webhook state file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict webhook state file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close webhook state file: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecar := name + suffix
		info, err := os.Lstat(sidecar)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect webhook state sidecar: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("webhook state sidecar %q must not be a symlink", sidecar)
		}
		if err := os.Chmod(sidecar, 0o600); err != nil {
			return fmt.Errorf("restrict webhook state sidecar: %w", err)
		}
	}
	return nil
}

func (w *WebhookOutput) openQueue() error {
	name, err := durableFileName(w.statePath)
	if err != nil {
		return err
	}
	if w.maxPending < 11 {
		return errors.New("max-pending must be at least 11 with state-path")
	}
	if err := secureStateFile(name); err != nil {
		return err
	}
	store, err := state.NewSqliteStore(w.statePath)
	if err != nil {
		return fmt.Errorf("open webhook state: %w", err)
	}
	w.queue = store
	w.drainErr = nil
	fingerprint := sha256.Sum256([]byte(w.format + "\x00" + w.url))
	want := []byte(hex.EncodeToString(fingerprint[:]))
	current, found, err := store.Get(destinationKey)
	if err != nil {
		return fmt.Errorf("read webhook destination identity: %w", err)
	}
	if found && string(current) != string(want) {
		return errors.New("webhook state belongs to a different URL or format; use a separate state-path")
	}
	if !found {
		if err := store.Set(destinationKey, want); err != nil {
			return fmt.Errorf("persist webhook destination identity: %w", err)
		}
	}
	queueID, found, err := store.Get(queueIDKey)
	if err != nil {
		return fmt.Errorf("read webhook queue identity: %w", err)
	}
	if !found {
		identity := make([]byte, 16)
		if _, err := rand.Read(identity); err != nil {
			return fmt.Errorf("create webhook queue identity: %w", err)
		}
		queueID = []byte(hex.EncodeToString(identity))
		if err := store.Set(queueIDKey, queueID); err != nil {
			return fmt.Errorf("persist webhook queue identity: %w", err)
		}
	}
	if len(queueID) != 32 {
		return errors.New("invalid webhook queue identity")
	}
	w.queueID = string(queueID)
	count, err := store.QueueLen(queueName)
	if err != nil {
		return fmt.Errorf("count pending webhooks: %w", err)
	}
	if count > w.maxPending {
		return fmt.Errorf("pending webhook count %d exceeds max-pending %d", count, w.maxPending)
	}
	return nil
}

func (w *WebhookOutput) closeQueue() error {
	err := w.drainErr
	if w.queue != nil {
		err = errors.Join(err, w.queue.Close())
		w.queue = nil
	}
	return err
}

func validateDurableEvent(evt event.Event) error {
	if evt.Payload == nil {
		return fmt.Errorf("received event with nil payload (type %q)", evt.Type)
	}
	switch evt.Type {
	case event.TypeBlock:
		if _, ok := evt.Payload.(event.BlockEvent); !ok {
			return unexpectedPayloadErr(evt.Type, evt.Payload)
		}
		if _, ok := evt.Context.(event.BlockContext); !ok {
			return unexpectedContextErr(evt.Type, evt.Context)
		}
	case event.TypeRollback:
		if _, ok := evt.Payload.(event.RollbackEvent); !ok {
			return unexpectedPayloadErr(evt.Type, evt.Payload)
		}
	case event.TypeTransaction:
		if _, ok := evt.Payload.(event.TransactionEvent); !ok {
			return unexpectedPayloadErr(evt.Type, evt.Payload)
		}
		if _, ok := evt.Context.(event.TransactionContext); !ok {
			return unexpectedContextErr(evt.Type, evt.Context)
		}
	case event.TypeGovernance:
		if _, ok := evt.Payload.(event.GovernanceEvent); !ok {
			return unexpectedPayloadErr(evt.Type, evt.Payload)
		}
		if _, ok := evt.Context.(event.GovernanceContext); !ok {
			return unexpectedContextErr(evt.Type, evt.Context)
		}
	default:
		return fmt.Errorf("received unknown event type %q", evt.Type)
	}
	return nil
}

func (w *WebhookOutput) runDurable(ctx context.Context) {
	for evt := range w.Input() {
		if err := validateDurableEvent(evt); err != nil {
			w.reportError(w.log(), err)
			continue
		}
		body, err := formatWebhook(&evt, w.format)
		if err != nil {
			w.reportError(w.log(), err)
			continue
		}
		encoded, err := json.Marshal(queuedWebhook{Type: evt.Type, Body: body})
		if err != nil {
			w.reportError(w.log(), fmt.Errorf("encode queued webhook: %w", err))
			continue
		}
		if len(encoded) > maxQueuedBytes {
			w.drainErr = errors.Join(w.drainErr, fmt.Errorf("webhook payload exceeds %d-byte durable queue limit", maxQueuedBytes))
			if ctx.Err() == nil {
				w.Fail(w.drainErr)
				<-w.Done()
			}
			continue
		}
		if _, err := w.queue.Enqueue(queueName, encoded, w.maxPending); err != nil {
			w.drainErr = errors.Join(w.drainErr, fmt.Errorf("persist webhook delivery: %w", err))
			if ctx.Err() == nil {
				w.Fail(w.drainErr)
				<-w.Done()
			}
			continue
		}
		if ctx.Err() == nil {
			if err := w.replay(ctx); err != nil {
				terminal := ctx.Err() == nil
				w.handleReplayError(ctx, err)
				if terminal {
					<-w.Done()
				}
			}
		}
	}
}

func (w *WebhookOutput) handleReplayError(ctx context.Context, err error) {
	if err == nil || (ctx.Err() != nil && errors.Is(err, ctx.Err())) {
		return
	}
	if ctx.Err() == nil {
		w.Fail(err)
	} else {
		w.drainErr = errors.Join(w.drainErr, err)
	}
}

func (w *WebhookOutput) replay(ctx context.Context) error {
	for ctx.Err() == nil {
		entry, found, err := w.queue.Peek(queueName)
		if err != nil {
			return fmt.Errorf("read pending webhook: %w", err)
		}
		if !found {
			return nil
		}
		var pending queuedWebhook
		if err := json.Unmarshal(entry.Value, &pending); err != nil {
			return fmt.Errorf("decode pending webhook %d: %w", entry.ID, err)
		}
		if pending.Type == "" || len(pending.Body) == 0 {
			return fmt.Errorf("pending webhook %d has no type or body", entry.ID)
		}
		if err := w.sendWithRetry(ctx, w.Done(), pending.Type, func(ctx context.Context) deliveryAttempt {
			return w.sendWebhookData(ctx, pending.Type, pending.Body, w.queueID+"-"+strconv.FormatInt(entry.ID, 10))
		}); err != nil {
			return err
		}
		if err := w.queue.Ack(queueName, entry.ID); err != nil {
			return fmt.Errorf("acknowledge webhook %d: %w", entry.ID, err)
		}
	}
	return ctx.Err()
}
