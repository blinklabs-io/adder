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
	const schema = `CREATE TABLE IF NOT EXISTS state (
		key   TEXT PRIMARY KEY,
		value BLOB NOT NULL
	)`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: create sqlite schema: %w", err)
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

// Close implements Store.
func (s *SqliteStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("state: close sqlite store: %w", err)
	}
	return nil
}
