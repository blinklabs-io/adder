// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package state

import (
	"errors"
	"time"
)

var ErrQueueFull = errors.New("state: queue is full")

// QueueEntry is a persistently ordered item awaiting acknowledgement.
type QueueEntry struct {
	ID        int64
	CreatedAt time.Time
	Value     []byte
}

// QueueStore supports bounded append, ordered replay, and explicit acknowledgement.
type QueueStore interface {
	Enqueue(queue string, value []byte, limit int) (int64, error)
	Peek(queue string) (QueueEntry, bool, error)
	Ack(queue string, id int64) error
	QueueLen(queue string) (int, error)
}
