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

package state

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SqliteStore is a Store backed by a SQLite database file.
type SqliteStore struct {
	db *sql.DB
}

// NewSqliteStore opens (creating if necessary) a SQLite database at path
// and ensures its schema exists. Use ":memory:" for a store scoped to the
// process, which is useful in tests.
func NewSqliteStore(path string) (*SqliteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("state: open sqlite store: %w", err)
	}
	// SQLite allows only one writer at a time. A single connection avoids
	// "database is locked" errors from concurrent Set calls racing each
	// other across separate connections in the pool.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: configure sqlite busy timeout: %w", err)
	}
	const schema = `CREATE TABLE IF NOT EXISTS state (
		key   TEXT PRIMARY KEY,
		value BLOB NOT NULL
	)`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: create sqlite schema: %w", err)
	}
	const queueSchema = `CREATE TABLE IF NOT EXISTS queue (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		value BLOB NOT NULL
	);
	CREATE INDEX IF NOT EXISTS queue_name_id ON queue(name, id)`
	if _, err := db.Exec(queueSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: create sqlite queue schema: %w", err)
	}
	return &SqliteStore{db: db}, nil
}

// Get implements Store.
func (s *SqliteStore) Get(key string) ([]byte, bool, error) {
	var value []byte
	err := s.db.QueryRow(`SELECT value FROM state WHERE key = ?`, key).
		Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("state: get %q: %w", key, err)
	}
	return value, true, nil
}

// Set implements Store.
func (s *SqliteStore) Set(key string, value []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key,
		value,
	)
	if err != nil {
		return fmt.Errorf("state: set %q: %w", key, err)
	}
	return nil
}

// Enqueue appends value if queue has fewer than limit pending entries.
func (s *SqliteStore) Enqueue(queue string, value []byte, limit int) (int64, error) {
	if queue == "" || limit < 1 {
		return 0, errors.New("state: queue name and positive limit are required")
	}
	// One statement holds SQLite's write lock while checking the bound and
	// inserting. A deferred read transaction would need a lock upgrade and
	// can return SQLITE_BUSY when another store writes the same database.
	result, err := s.db.Exec(`INSERT INTO queue(name, created_at, value)
		SELECT ?, ?, ? WHERE (SELECT COUNT(*) FROM queue WHERE name = ?) < ?`,
		queue, time.Now().UnixNano(), value, queue, limit)
	if err != nil {
		return 0, fmt.Errorf("state: append queue: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("state: queue append result: %w", err)
	}
	if rows == 0 {
		return 0, ErrQueueFull
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("state: queue entry ID: %w", err)
	}
	return id, nil
}

// Peek returns the oldest pending entry in queue.
func (s *SqliteStore) Peek(queue string) (QueueEntry, bool, error) {
	var entry QueueEntry
	var createdAt int64
	err := s.db.QueryRow(`SELECT id, created_at, value FROM queue WHERE name = ? ORDER BY id LIMIT 1`, queue).Scan(&entry.ID, &createdAt, &entry.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return QueueEntry{}, false, nil
	}
	if err != nil {
		return QueueEntry{}, false, fmt.Errorf("state: peek queue: %w", err)
	}
	entry.CreatedAt = time.Unix(0, createdAt)
	return entry, true, nil
}

// Ack removes one previously delivered entry.
func (s *SqliteStore) Ack(queue string, id int64) error {
	result, err := s.db.Exec(`DELETE FROM queue WHERE name = ? AND id = ?`, queue, id)
	if err != nil {
		return fmt.Errorf("state: ack queue entry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("state: ack result: %w", err)
	}
	if count != 1 {
		return errors.New("state: queue entry was not pending")
	}
	return nil
}

// QueueLen returns the number of unacknowledged entries in queue.
func (s *SqliteStore) QueueLen(queue string) (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM queue WHERE name = ?`, queue).Scan(&count); err != nil {
		return 0, fmt.Errorf("state: count queue: %w", err)
	}
	return count, nil
}

// Close implements Store.
func (s *SqliteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("state: close sqlite store: %w", err)
	}
	return nil
}

var _ QueueStore = (*SqliteStore)(nil)
