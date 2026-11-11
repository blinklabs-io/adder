// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package elasticsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestElasticsearchRequestContract(t *testing.T) {
	var mu sync.Mutex
	paths := []string{}
	bodies := [][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		count := len(paths)
		mu.Unlock()
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		if count == 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	o := New(WithURL(server.URL), WithIndex("adder-events-v1"))
	o.client = server.Client()
	require.NoError(t, o.Start())
	evt := event.New(event.TypeRollback, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), nil, map[string]any{"slot": 42})
	o.InputChan() <- evt
	// Wait until the retry succeeds before comparing its deterministic ID.
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(paths) == 3 }, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, o.Stop())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "/adder-events-v1", paths[0])
	require.Equal(t, paths[1], paths[2])
	require.True(t, strings.HasPrefix(paths[1], "/adder-events-v1/_doc/"))
	var mapping map[string]any
	require.NoError(t, json.Unmarshal(bodies[0], &mapping))
	require.Equal(t, "strict", mapping["mappings"].(map[string]any)["dynamic"])
	var doc map[string]any
	require.NoError(t, json.Unmarshal(bodies[2], &doc))
	require.Equal(t, float64(1), doc["schema_version"])
	require.Equal(t, event.TypeRollback, doc["event_type"])
}

func TestElasticsearchErrorsAndValidation(t *testing.T) {
	for _, v := range []map[string]any{
		{"url": "ftp://localhost", "index": "adder-events-v1"},
		{"url": "http://localhost:9200", "api-key": "secret"},
		{"url": "http://localhost:9200", "index": "BadIndex"},
		{"url": "http://localhost:9200", "index": "a/b"},
		{"url": "http://localhost:9200", "event-types": "unknown"},
	} {
		_, err := plugin.GetPlugin(plugin.PluginTypeOutput, "elasticsearch", v)
		require.Error(t, err)
	}
	p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "elasticsearch", map[string]any{"url": "http://127.0.0.1:9200", "event-types": "rollbacks"})
	require.NoError(t, err)
	require.True(t, p.(*Output).selected[event.TypeRollback])
	require.False(t, p.(*Output).selected[event.TypeBlock])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"mapping rejected"}`))
	}))
	defer server.Close()
	o := New(WithURL(server.URL))
	o.client = server.Client()
	require.ErrorContains(t, o.Start(), "mapping rejected")
	id, doc, err := document(event.New(event.TypeBlock, time.Now(), nil, map[string]any{"height": 1}))
	require.NoError(t, err)
	require.Len(t, id, 64)
	require.True(t, json.Valid(doc))
	_, _, err = document(event.New(event.TypeBlock, time.Now(), nil, make(chan int)))
	require.Error(t, err)
	_, _, err = o.request(context.Background(), http.MethodPut, "/%", []byte(`{}`))
	require.Error(t, err)
}

func TestElasticsearchExistingMappingAndEscapedIndex(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "/_doc/") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"resource_already_exists_exception"}}`))
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"events%":{"mappings":{"dynamic":"strict","properties":{"schema_version":{"type":"integer"},"event_type":{"type":"keyword"},"event_time":{"type":"date"},"event":{"type":"object","enabled":false}}}}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	o := New(WithURL(server.URL), WithIndex("events%"))
	o.client = server.Client()
	require.NoError(t, o.Start())
	require.Contains(t, gotPath, "events%25")
	require.NoError(t, o.Stop())
}

func TestElasticsearchRejectsIncompatibleMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"resource_already_exists_exception"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"adder-events-v1":{"mappings":{"dynamic":"true","properties":{"event":{"type":"object","enabled":true}}}}}`))
	}))
	defer server.Close()
	o := New(WithURL(server.URL))
	o.client = server.Client()
	require.ErrorContains(t, o.Start(), "incompatible event mapping")
}

func TestElasticsearchWorkerFilteringAndFailure(t *testing.T) {
	docCalls := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/_doc/") {
			docCalls <- struct{}{}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad document"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	o := New(WithURL(server.URL), WithEventTypes(map[string]bool{event.TypeRollback: true}))
	o.client = server.Client()
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, nil)
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, map[string]any{"slot": 1})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "bad document")
	case <-time.After(2 * time.Second):
		t.Fatal("missing indexing error")
	}
	select {
	case <-docCalls:
	default:
		t.Fatal("missing document request")
	}
	select {
	case <-docCalls:
		t.Fatal("filtered block was indexed")
	default:
	}
	require.NoError(t, o.Stop())
}

func TestElasticsearchStopCancelsDocumentRequest(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/_doc/") {
			w.WriteHeader(http.StatusCreated)
			return
		}
		close(entered)
		<-release
	}))
	defer server.Close()
	defer close(release)
	o := New(WithURL(server.URL))
	o.client = server.Client()
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, nil)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	done := make(chan error, 1)
	go func() { done <- o.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel request")
	}
}

func TestElasticsearchDoesNotForwardAPIKeyOnRedirect(t *testing.T) {
	forwarded := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded <- r.Header.Clone()
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	o := New(WithURL(source.URL), WithAPIKey("secret"))
	o.client = source.Client()
	status, _, err := o.request(context.Background(), http.MethodPut, "/adder-events-v1", []byte(indexMapping))
	require.NoError(t, err)
	require.Equal(t, http.StatusTemporaryRedirect, status)
	select {
	case h := <-forwarded:
		t.Fatalf("API key reached redirect target: %v", h)
	default:
	}
}
