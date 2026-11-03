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
	"encoding/hex"
	"encoding/json"
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

// loadCursor replaces the configured intersect point(s) with the cursor
// persisted in stateStore, if one exists. It must run before the first
// connect, so a restart resumes from the last confirmed block instead of
// the configured intersect-tip/intersect-point setting.
func (c *ChainSync) loadCursor() {
	if c.stateStore == nil {
		return
	}
	logger := c.Logger()
	data, ok, err := c.stateStore.Get(c.stateKey)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to load persisted chainsync cursor", "error", err)
		}
		return
	}
	if !ok {
		return
	}
	var rec cursorRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		if logger != nil {
			logger.Warn("failed to parse persisted chainsync cursor", "error", err)
		}
		return
	}
	hashBytes, err := hex.DecodeString(rec.Hash)
	if err != nil {
		if logger != nil {
			logger.Warn(
				"failed to decode persisted chainsync cursor hash",
				"error",
				err,
			)
		}
		return
	}
	c.intersectPoints = []ocommon.Point{{Slot: rec.Slot, Hash: hashBytes}}
	c.intersectTip = false
	if logger != nil {
		logger.Info(
			"resuming chainsync from persisted cursor",
			"slot", rec.Slot,
			"hash", rec.Hash,
		)
	}
}

// saveCursor persists point to stateStore under stateKey.
func (c *ChainSync) saveCursor(point ocommon.Point) {
	rec := cursorRecord{Slot: point.Slot, Hash: hex.EncodeToString(point.Hash)}
	data, err := json.Marshal(rec)
	if err != nil {
		if logger := c.Logger(); logger != nil {
			logger.Warn("failed to encode chainsync cursor", "error", err)
		}
		return
	}
	if err := c.stateStore.Set(c.stateKey, data); err != nil {
		if logger := c.Logger(); logger != nil {
			logger.Warn("failed to persist chainsync cursor", "error", err)
		}
	}
}

// flushCursor persists the most recent cursor recorded since the last
// flush, if any. It is safe to call even when no cursor is pending or no
// state store is configured.
func (c *ChainSync) flushCursor() {
	if c.stateStore == nil {
		return
	}
	c.stateMu.Lock()
	point := c.pendingCursor
	c.pendingCursor = nil
	c.stateMu.Unlock()
	if point != nil {
		c.saveCursor(*point)
	}
}

// persistCursorLoop periodically flushes the cursor recorded by
// updateStatus to stateStore, rather than writing on every block, which
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
			c.flushCursor()
			return
		case <-done:
			c.flushCursor()
			return
		case <-ticker.C:
			c.flushCursor()
		}
	}
}
