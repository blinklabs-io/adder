// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package clickhouse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type receivedBatch struct {
	query                        url.Values
	body                         []byte
	username, password, database string
}

func TestClickHouseShutdownFlushAndRollback(t *testing.T) {
	batches := make(chan receivedBatch, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if strings.Contains(r.URL.Query().Get("query"), "INSERT") {
			batches <- receivedBatch{r.URL.Query(), body, r.Header.Get("X-ClickHouse-User"), r.Header.Get("X-ClickHouse-Key"), r.Header.Get("X-ClickHouse-Database")}
		} else if !strings.Contains(r.URL.Query().Get("query"), "CREATE TABLE") {
			t.Errorf("unexpected query: %s", r.URL.Query().Get("query"))
		} else if got := r.URL.Query().Get("deduplicate_insert"); got != "disable" {
			t.Errorf("startup deduplication setting = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	o := New(WithURL(server.URL), WithDatabase("analytics"), WithUsername("adder"), WithEventTypes(map[string]bool{event.TypeBlock: true, event.TypeRollback: true}))
	o.client = server.Client()
	require.NoError(t, o.Start())
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	block := event.New(event.TypeBlock, stamp, nil, map[string]any{"hash": "same"})
	o.InputChan() <- block
	o.InputChan() <- event.New(event.TypeRollback, stamp, nil, map[string]any{"slot": 1})
	o.InputChan() <- block
	o.InputChan() <- event.New(event.TypeTransaction, stamp, nil, nil)
	require.NoError(t, o.Stop())
	select {
	case batch := <-batches:
		require.Equal(t, "adder", batch.username)
		require.Equal(t, "analytics", batch.database)
		require.Empty(t, batch.password)
		require.Equal(t, "1", batch.query.Get("wait_end_of_query"))
		require.Equal(t, "0", batch.query.Get("insert_deduplicate"))
		require.Equal(t, "disable", batch.query.Get("deduplicate_insert"))
		require.Equal(t, "0", batch.query.Get("async_insert"))
		lines := strings.Split(strings.TrimSpace(string(batch.body)), "\n")
		require.Len(t, lines, 3)
		var first, last map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
		require.NoError(t, json.Unmarshal([]byte(lines[2]), &last))
		require.Equal(t, first["event_hash"], last["event_hash"])
		require.Equal(t, event.TypeBlock, first["event_type"])
		require.Equal(t, "2026-01-02 03:04:05.000000123", first["event_time"])
	case <-time.After(2 * time.Second):
		t.Fatal("missing final batch")
	}
}

func TestClickHouseTimedFlushAndLateError(t *testing.T) {
	batches := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "INSERT") {
			body, _ := io.ReadAll(r.Body)
			batches <- body
			_, _ = w.Write([]byte("Code: 395. DB::Exception: rejected"))
		}
	}))
	defer server.Close()
	o := New(WithURL(server.URL))
	o.client = server.Client()
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, nil)
	select {
	case <-batches:
	case <-time.After(2 * time.Second):
		t.Fatal("timed flush did not occur")
	}
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "rejected")
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP 200 exception was ignored")
	}
	require.NoError(t, o.Stop())
}

func TestClickHouseRowAndConfig(t *testing.T) {
	evt := event.New(event.TypeRollback, time.Now(), nil, map[string]any{"slot": 42})
	row, err := encodeRow(evt)
	require.NoError(t, err)
	require.True(t, json.Valid(row))
	_, err = encodeRow(event.New(event.TypeBlock, time.Now(), nil, make(chan int)))
	require.Error(t, err)
	for _, v := range []map[string]any{
		{"url": "ftp://localhost"},
		{"url": "http://example.org:8123", "password": "secret"},
		{"url": "http://127.0.0.1:8123", "event-types": "unknown"},
		{"url": "http://127.0.0.1:8123", "event-types": ""},
		{"url": "http://127.0.0.1:8123", "database": ""},
	} {
		_, err := plugin.GetPlugin(plugin.PluginTypeOutput, "clickhouse", v)
		require.Error(t, err)
	}
	p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "clickhouse", map[string]any{"url": "http://127.0.0.1:8123", "event-types": "rollbacks"})
	require.NoError(t, err)
	require.True(t, p.(*Output).selected[event.TypeRollback])
	require.False(t, p.(*Output).selected[event.TypeBlock])
	require.NoError(t, New(WithURL("http://[::1]:8123"), WithPassword("secret")).validate())
}

func TestClickHouseDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	forwarded := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded <- r.Header.Clone()
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	o := New(WithURL(source.URL), WithUsername("adder"), WithPassword("secret"))
	o.client = source.Client()
	err := o.query(context.Background(), createSQL, nil, false)
	require.ErrorContains(t, err, "307")
	select {
	case h := <-forwarded:
		t.Fatalf("credentials reached redirect target: %v", h)
	default:
	}
}

func TestClickHouseFlushesAtHundredEvents(t *testing.T) {
	batches := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "INSERT") {
			body, _ := io.ReadAll(r.Body)
			batches <- body
		}
	}))
	defer server.Close()
	o := New(WithURL(server.URL))
	o.client = server.Client()
	require.NoError(t, o.Start())
	for i := 0; i < maxBatch; i++ {
		o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, map[string]any{"i": i})
	}
	select {
	case body := <-batches:
		require.Len(t, strings.Split(strings.TrimSpace(string(body)), "\n"), maxBatch)
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not flush at 100 events")
	}
	require.NoError(t, o.Stop())
}
