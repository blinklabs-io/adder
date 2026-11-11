// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type signalingMatcher struct {
	entered chan struct{}
	once    sync.Once
}

func (m *signalingMatcher) Match(expected, actual string) error {
	if strings.Contains(expected, "INSERT INTO") {
		m.once.Do(func() { close(m.entered) })
	}
	return sqlmock.QueryMatcherRegexp.Match(expected, actual)
}

func TestPostgreSQLInsertContract(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	o := New(WithURL("postgres://adder:secret@localhost:5432/adder?sslmode=require"))
	o.db = db
	evt := event.New(event.TypeRollback, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), nil, map[string]any{"slot": 42})
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	query := regexp.QuoteMeta(insertSQL)
	mock.ExpectExec(query).WithArgs(eventHash(data), event.TypeRollback, evt.Timestamp, string(data)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(query).WithArgs(eventHash(data), event.TypeRollback, evt.Timestamp, string(data)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, o.persist(context.Background(), evt))
	require.NoError(t, o.persist(context.Background(), evt))
	changed := evt
	changed.Payload = map[string]any{"slot": 43}
	changedData, err := json.Marshal(changed)
	require.NoError(t, err)
	require.NotEqual(t, eventHash(data), eventHash(changedData))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgreSQLWorkerErrorsAndFiltering(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	o := New(WithURL("postgres://adder:secret@localhost:5432/adder?sslmode=require"), WithEventTypes(map[string]bool{event.TypeRollback: true}))
	o.db = db
	mock.ExpectPing()
	mock.ExpectExec(regexp.QuoteMeta(schemaSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(indexSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(insertSQL)).WillReturnError(errors.New("database unavailable"))
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, nil)
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, map[string]any{"slot": 42})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "database unavailable")
	case <-time.After(2 * time.Second):
		t.Fatal("missing insert error")
	}
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, make(chan int))
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "encode PostgreSQL event")
	case <-time.After(2 * time.Second):
		t.Fatal("missing marshal error")
	}
	mock.ExpectClose()
	require.NoError(t, o.Stop())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgreSQLConfiguration(t *testing.T) {
	for _, v := range []map[string]any{nil, {"database-url": "invalid"}, {"database-url": "postgres://a:b@localhost/db", "event-types": "rollbacks"}, {"database-url": "postgres://a:b@localhost/db?sslmode=disable", "event-types": "rollbacks"}, {"database-url": "postgres://a:b@localhost/db?sslmode=require", "event-types": "unknown"}, {"database-url": "postgres://a:b@localhost/db?sslmode=require", "event-types": ""}} {
		_, err := plugin.GetPlugin(plugin.PluginTypeOutput, "postgres", v)
		require.Error(t, err)
	}
	p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "postgres", map[string]any{"database-url": "postgres://a:b@localhost/db?sslmode=require", "event-types": "rollbacks"})
	require.NoError(t, err)
	require.True(t, p.(*Output).selected[event.TypeRollback])
	require.False(t, p.(*Output).selected[event.TypeBlock])
}

func TestPostgreSQLStopCancelsInsertAndDropsQueue(t *testing.T) {
	matcher := &signalingMatcher{entered: make(chan struct{})}
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true), sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	o := New(WithURL("postgres://adder:secret@localhost:5432/adder?sslmode=require"))
	o.db = db
	mock.ExpectPing()
	mock.ExpectExec(regexp.QuoteMeta(schemaSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(indexSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(insertSQL)).WillDelayFor(5 * time.Second).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, map[string]any{"slot": 1})
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, map[string]any{"slot": 2})
	select {
	case <-matcher.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("insert did not begin")
	}
	mock.ExpectClose()
	stopped := make(chan error, 1)
	go func() { stopped <- o.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel insert")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
