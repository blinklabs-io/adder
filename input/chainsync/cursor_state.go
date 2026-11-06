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
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

// cursorRecord is the JSON representation of a persisted chainsync
// cursor. The hash is hex-encoded so the record round-trips through
// state.Store, which only deals in raw bytes, without surprising a human
// reading the underlying store directly.
type cursorRecord struct {
	Slot uint64 `json:"slot"`
	Hash string `json:"hash"`
}

func (c *ChainSync) ensureStateStore() error {
	if c.stateStore != nil || c.openStateStore == nil {
		return nil
	}
	store, err := c.openStateStore()
	if err != nil {
		return fmt.Errorf("open chainsync state store: %w", err)
	}
	c.stateStore = store
	return nil
}

// loadCursor replaces the configured intersect point(s) with the cursor
// persisted in stateStore, if one exists. It must run before the first
// connect, so a restart resumes from the last emitted block instead of
// the configured intersect-tip/intersect-point setting.
func (c *ChainSync) loadCursor() error {
	if c.stateStore == nil {
		return nil
	}
	data, ok, err := c.stateStore.Get(c.stateKey)
	if err != nil {
		return fmt.Errorf("read persisted chainsync cursor: %w", err)
	}
	if !ok {
		return nil
	}
	var rec cursorRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return fmt.Errorf("parse persisted chainsync cursor: %w", err)
	}
	hashBytes, err := hex.DecodeString(rec.Hash)
	if err != nil {
		return fmt.Errorf("decode persisted chainsync cursor hash: %w", err)
	}
	if len(hashBytes) != 32 && (rec.Slot != 0 || len(hashBytes) != 0) {
		return fmt.Errorf(
			"invalid persisted chainsync cursor hash length: got %d, want 32",
			len(hashBytes),
		)
	}
	c.intersectPoints = []ocommon.Point{{Slot: rec.Slot, Hash: hashBytes}}
	c.intersectTip = false
	// Treat the persisted checkpoint as an emitted point for rollback history.
	// A restart can receive a rollback before it emits a new block; without
	// this seed, that rollback would incorrectly fall back to genesis.
	c.stateMu.Lock()
	c.emittedCursors = []ocommon.Point{{Slot: rec.Slot, Hash: hashBytes}}
	c.stateMu.Unlock()
	if logger := c.Logger(); logger != nil {
		logger.Info(
			"resuming chainsync from persisted cursor",
			"slot", rec.Slot,
			"hash", rec.Hash,
		)
	}
	return nil
}

// saveCursor persists point to stateStore under stateKey.
func (c *ChainSync) saveCursor(point ocommon.Point) error {
	rec := cursorRecord{Slot: point.Slot, Hash: hex.EncodeToString(point.Hash)}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode chainsync cursor: %w", err)
	}
	if err := c.stateStore.Set(c.stateKey, data); err != nil {
		return fmt.Errorf("persist chainsync cursor: %w", err)
	}
	return nil
}

func (c *ChainSync) setPendingCursor(point ocommon.Point) {
	if c.stateStore == nil {
		return
	}
	c.stateMu.Lock()
	c.emittedCursors = append(c.emittedCursors, point)
	if len(c.emittedCursors) > cursorCacheSize {
		c.emittedCursors = c.emittedCursors[len(c.emittedCursors)-cursorCacheSize:]
	}
	c.pendingCursor = &point
	c.stateMu.Unlock()
}

func (c *ChainSync) rewindPendingCursor(rollback ocommon.Point) {
	if c.stateStore == nil {
		return
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	for len(c.emittedCursors) > 0 {
		last := c.emittedCursors[len(c.emittedCursors)-1]
		if last.Slot < rollback.Slot ||
			(last.Slot == rollback.Slot && bytes.Equal(last.Hash, rollback.Hash)) {
			point := last
			c.pendingCursor = &point
			return
		}
		c.emittedCursors = c.emittedCursors[:len(c.emittedCursors)-1]
	}
	// No emitted block survives the rollback. Persist the genesis point so
	// a previously saved cursor cannot make the next run skip buffered data.
	point := ocommon.Point{}
	c.pendingCursor = &point
}

// flushCursor persists the most recent cursor recorded since the last
// flush, if any. It is safe to call even when no cursor is pending or no
// state store is configured.
func (c *ChainSync) flushCursor() error {
	if c.stateStore == nil {
		return nil
	}
	c.stateMu.Lock()
	point := c.pendingCursor
	c.stateMu.Unlock()
	if point == nil {
		return nil
	}
	if err := c.saveCursor(*point); err != nil {
		return err
	}
	c.stateMu.Lock()
	if c.pendingCursor != nil &&
		c.pendingCursor.Slot == point.Slot &&
		bytes.Equal(c.pendingCursor.Hash, point.Hash) {
		c.pendingCursor = nil
	}
	c.stateMu.Unlock()
	return nil
}

// persistCursorLoop periodically flushes the cursor recorded for the
// last emitted block to stateStore, rather than writing on every block, which
// would add a disk write per block during catch-up sync. It runs for the
// life of the connection and performs a final flush on exit so the last
// confirmed block is never lost on a clean shutdown.
func (c *ChainSync) persistCursorLoop(ctx context.Context) {
	ticker := time.NewTicker(cursorPersistInterval)
	defer ticker.Stop()
	done := c.Done()
	for {
		select {
		case <-ctx.Done():
			_ = c.flushCursor()
			return
		case <-done:
			_ = c.flushCursor()
			return
		case <-ticker.C:
			_ = c.flushCursor()
		}
	}
}
