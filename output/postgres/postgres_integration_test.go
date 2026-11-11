// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

// Set ADDER_TEST_POSTGRES_URL to a dedicated PostgreSQL database to verify
// schema creation and ordered rollback replay against a real server.
func TestPostgreSQLIntegration(t *testing.T) {
	url := os.Getenv("ADDER_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("set ADDER_TEST_POSTGRES_URL for PostgreSQL integration")
	}
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, schemaSQL)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, indexSQL)
	require.NoError(t, err)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	block := event.New(event.TypeBlock, stamp, nil, map[string]any{"hash": "same-block"})
	rollback := event.New(event.TypeRollback, stamp, nil, map[string]any{"slot": 1})
	for _, evt := range []event.Event{block, rollback, block} {
		data, err := json.Marshal(evt)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, insertSQL, eventHash(data), evt.Type, evt.Timestamp, data)
		require.NoError(t, err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_type, event_hash FROM adder_events ORDER BY arrival_id DESC LIMIT 3`)
	require.NoError(t, err)
	defer rows.Close()
	var types, hashes []string
	for rows.Next() {
		var typ, hash string
		require.NoError(t, rows.Scan(&typ, &hash))
		types, hashes = append(types, typ), append(hashes, hash)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{event.TypeBlock, event.TypeRollback, event.TypeBlock}, types)
	if len(hashes) != 3 { t.Fatalf("expected three event hashes, got %d", len(hashes)) }
	require.Equal(t, hashes[0], hashes[2])
}
