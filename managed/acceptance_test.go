// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	_ "github.com/blinklabs-io/adder/output"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type diagnosticCapture struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func (c *diagnosticCapture) Write(data []byte) (int, error) {
	c.mu.Lock()
	n, err := c.buffer.Write(data)
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (c *diagnosticCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.String()
}

type routeRecord struct {
	Action      string   `json:"action"`
	Customer    string   `json:"customer_id"`
	Destination string   `json:"destination_id"`
	Rule        string   `json:"rule_id"`
	Rules       []string `json:"rule_ids"`
	Revision    uint64   `json:"revision"`
	Occurrence  uint64   `json:"occurrence_id"`
}

func (c *diagnosticCapture) records(t *testing.T) []routeRecord {
	t.Helper()
	var records []routeRecord
	scanner := bufio.NewScanner(strings.NewReader(c.text()))
	for scanner.Scan() {
		var record routeRecord
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		records = append(records, record)
	}
	require.NoError(t, scanner.Err())
	return records
}

func (c *diagnosticCapture) await(t *testing.T, condition func([]routeRecord) bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if condition(c.records(t)) {
			return
		}
		select {
		case <-c.changed:
		case <-timer.C:
			t.Fatal("timed out waiting for router diagnostic", c.text())
		}
	}
}

func countAction(records []routeRecord, action, customer string) int {
	n := 0
	for _, record := range records {
		if record.Action == action && (customer == "" || record.Customer == customer) {
			n++
		}
	}
	return n
}

type acceptanceBundle struct {
	options     Options
	diagnostics *diagnosticCapture
}

func newAcceptanceBundle(t *testing.T) *acceptanceBundle {
	t.Helper()
	root := t.TempDir()
	capture := &diagnosticCapture{changed: make(chan struct{}, 1)}
	b := &acceptanceBundle{diagnostics: capture, options: Options{
		ConfigDir: filepath.Join(root, "config"), CredentialsDir: filepath.Join(root, "credentials"), LogDir: filepath.Join(root, "logs"),
		AllowedHosts: []string{"127.0.0.1"}, AllowLocalTargets: true,
		Logger: slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}}
	for _, path := range []string{b.options.ConfigDir, b.options.CredentialsDir, b.options.LogDir} {
		require.NoError(t, os.Mkdir(path, 0o700))
	}
	return b
}

func publishJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	temporary := path + ".tmp"
	require.NoError(t, os.WriteFile(temporary, data, 0o600))
	require.NoError(t, os.Rename(temporary, path))
}

func (b *acceptanceBundle) publish(t *testing.T, generation uint64, customers ...Customer) {
	t.Helper()
	manifest := Manifest{Generation: generation}
	for _, customer := range customers {
		name := customer.ID + ".json"
		publishJSON(t, filepath.Join(b.options.ConfigDir, name), customer)
		manifest.Customers = append(manifest.Customers, CustomerRef{ID: customer.ID, Revision: customer.Revision, File: name})
	}
	publishJSON(t, filepath.Join(b.options.ConfigDir, "manifest.json"), manifest)
}

func localCustomer(id string) Customer {
	return Customer{
		ID: id, Revision: 1,
		Destinations: []Destination{{ID: "local", Plugin: "log", Options: map[string]string{"path": id + ".jsonl", "format": "json"}}},
		Filters:      []Rule{{ID: "blocks", EventType: event.TypeBlock, Destination: "all"}},
	}
}

func (b *acceptanceBundle) webhook(t *testing.T, customer, name, target string, version uint64) Destination {
	t.Helper()
	parsed, err := url.Parse(target)
	require.NoError(t, err)
	file := fmt.Sprintf("%s-%s-%d.json", customer, name, version)
	publishJSON(t, filepath.Join(b.options.CredentialsDir, file), map[string]any{"customer_id": customer, "version": version, "value": target})
	return Destination{ID: name, Plugin: "webhook", Options: map[string]string{"format": "adder"}, Credentials: map[string]CredentialRef{"url": {File: file, Version: version}}, LimitGroup: parsed.Host, IntervalMS: 1}
}

func (b *acceptanceBundle) start(t *testing.T, configure func(*Router)) *Router {
	t.Helper()
	r, err := New(b.options)
	require.NoError(t, err)
	if configure != nil {
		configure(r)
	}
	require.NoError(t, r.StartContext(context.Background()))
	t.Cleanup(func() { require.NoError(t, r.Stop()) })
	return r
}

func block(label string) event.Event {
	return event.Event{Type: event.TypeBlock, Context: event.BlockContext{}, Payload: event.BlockEvent{BlockHash: label}}
}

func receiptLabels(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var labels []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var receipt struct {
			Payload struct {
				Label string `json:"blockHash"`
			} `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &receipt))
		labels = append(labels, receipt.Payload.Label)
	}
	require.NoError(t, scanner.Err())
	return labels
}

type observedHTTP struct{ path, label string }

func TestManagedSyntheticExactOracle(t *testing.T) {
	received := make(chan observedHTTP, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Payload struct {
				Label string `json:"blockHash"`
			} `json:"payload"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		received <- observedHTTP{req.URL.Path, body.Payload.Label}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	b := newAcceptanceBundle(t)
	customers := make([]Customer, 5)
	for i := range customers {
		id := fmt.Sprintf("u%d", i+1)
		c := localCustomer(id)
		c.Destinations = append(c.Destinations, b.webhook(t, id, "remote", server.URL+"/"+id+"?secret=CREDENTIAL_SENTINEL", 1))
		c.Filters = []Rule{{ID: "primary", EventType: event.TypeBlock, Destination: "all"}, {ID: "overlap", EventType: event.TypeBlock, Destination: "remote"}, {ID: "rollback", EventType: event.TypeRollback, Destination: "local"}}
		customers[i] = c
	}
	b.publish(t, 1, customers...)
	r := b.start(t, nil)
	repeated := block("identical-payload-sentinel")
	r.dispatch(repeated)
	r.dispatch(repeated)
	r.dispatch(event.Event{Type: event.TypeRollback, Payload: event.RollbackEvent{SlotNumber: 1, BlockHash: "rollback"}})
	r.dispatch(event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{}, Payload: event.TransactionEvent{BlockHash: "forbidden"}})
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "") == 25 })
	customers[0].Revision = 2
	customers[0].Filters[0].EventType = event.TypeTransaction
	b.publish(t, 2, customers...)
	cutover, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(4), cutover.Cutover)
	r.dispatch(block("after-reload"))
	r.dispatch(event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{}, Payload: event.TransactionEvent{BlockHash: "new-transaction"}})
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "") == 36 })
	require.NoError(t, r.Stop())
	var actual, expected []string
	configured := map[string]bool{}
	for _, record := range b.diagnostics.records(t) {
		if record.Action == "filter_configured" && record.Revision == 1 {
			configured[record.Customer+"/"+record.Rule] = true
		}
		if record.Action == "submitted_to_plugin" {
			actual = append(actual, fmt.Sprintf("%s/%d/%s/%d/%s", record.Customer, record.Occurrence, record.Destination, record.Revision, strings.Join(record.Rules, ",")))
		}
	}
	require.Len(t, configured, 15)
	for _, id := range []string{"u1", "u2", "u3", "u4", "u5"} {
		for _, row := range []string{"1/local/1/primary", "1/remote/1/overlap,primary", "2/local/1/primary", "2/remote/1/overlap,primary", "3/local/1/rollback"} {
			expected = append(expected, id+"/"+row)
		}
		if id != "u1" {
			expected = append(expected, id+"/5/local/1/primary", id+"/5/remote/1/overlap,primary")
		}
	}
	expected = append(expected, "u1/5/remote/2/overlap", "u1/6/local/2/primary", "u1/6/remote/2/primary")
	slices.Sort(actual)
	slices.Sort(expected)
	require.Equal(t, expected, actual)
	for _, id := range []string{"u1", "u2", "u3", "u4", "u5"} {
		want := []string{"identical-payload-sentinel", "identical-payload-sentinel", "rollback", "after-reload"}
		if id == "u1" {
			want[3] = "new-transaction"
		}
		require.Equal(t, want, receiptLabels(t, filepath.Join(b.options.LogDir, id+".jsonl")))
	}
	remote := map[string][]string{}
	for len(received) > 0 {
		item := <-received
		remote[item.path] = append(remote[item.path], item.label)
	}
	for _, id := range []string{"u1", "u2", "u3", "u4", "u5"} {
		want := []string{"identical-payload-sentinel", "identical-payload-sentinel", "after-reload"}
		if id == "u1" {
			want = append(want, "new-transaction")
		}
		require.Equal(t, want, remote["/"+id])
	}
	require.Len(t, remote, 5)
	state := r.Status()
	require.Equal(t, uint64(36), state.Counters.SubmittedToPlugin)
	require.Equal(t, uint64(36), state.Counters.UnknownOutcomes)
	require.Zero(t, state.Counters.InputDropped)
	require.Zero(t, state.Counters.DestinationDropped)
	require.Zero(t, state.Counters.PluginErrors)
	require.Zero(t, state.PluginInstances)
	require.Zero(t, state.RetainedSize)
	require.NotContains(t, b.diagnostics.text(), "CREDENTIAL_SENTINEL")
	require.NotContains(t, b.diagnostics.text(), "identical-payload-sentinel")
}

func TestManagedBlockedCustomerDoesNotBlockOthers(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.InputQueue = 1
	b.publish(t, 1, localCustomer("u1"), localCustomer("u2"))
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := b.start(t, func(r *Router) {
		r.matchGate = func(ctx context.Context, id string) {
			if id == "u1" {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
		}
	})
	r.dispatch(block("one"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked worker not entered")
	}
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 1 })
	r.dispatch(block("two"))
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 2 })
	r.dispatch(block("three"))
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 3 })
	status := r.Status()
	require.Equal(t, uint64(1), status.Customers["u1"].Counters.InputDropped)
	require.Zero(t, status.Customers["u2"].Counters.InputDropped)
	close(release)
	require.NoError(t, r.Stop())
	require.Equal(t, []string{"one", "two"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
	require.Equal(t, []string{"one", "two", "three"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u2.jsonl")))
}

func TestManagedQueuedEventsKeepBoundRevision(t *testing.T) {
	b := newAcceptanceBundle(t)
	c := localCustomer("u1")
	b.publish(t, 1, c)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := b.start(t, func(r *Router) {
		r.matchGate = func(ctx context.Context, _ string) {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	r.dispatch(block("old-one"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not entered")
	}
	r.dispatch(block("old-two"))
	c.Revision = 2
	c.Filters[0].EventType = event.TypeTransaction
	b.publish(t, 2, c)
	result, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.Cutover)
	r.dispatch(block("not-matched"))
	r.dispatch(event.Event{Type: event.TypeTransaction, Context: event.TransactionContext{}, Payload: event.TransactionEvent{BlockHash: "new"}})
	close(release)
	require.NoError(t, r.Stop())
	require.Equal(t, []string{"old-one", "old-two", "new"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
	var actual []string
	for _, record := range b.diagnostics.records(t) {
		if record.Action == "submitted_to_plugin" {
			actual = append(actual, fmt.Sprintf("%d/%d", record.Occurrence, record.Revision))
		}
	}
	require.Equal(t, []string{"1/1", "2/1", "4/2"}, actual)
}

func TestManagedReloadContracts(t *testing.T) {
	b := newAcceptanceBundle(t)
	u1, u2 := localCustomer("u1"), localCustomer("u2")
	b.publish(t, 1, u1, u2)
	r := b.start(t, nil)
	initial, err := r.ReloadAll(context.Background(), 1)
	require.NoError(t, err)
	require.Zero(t, initial.Cutover)
	_, err = r.ReloadCustomer(context.Background(), "u1", 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), r.Status().PluginInstances)
	r.dispatch(block("before"))
	u1.Revision = 2
	u1.Filters[0].ID = "changed"
	b.publish(t, 2, u1, u2)
	result, err := r.ReloadCustomer(context.Background(), "u1", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.Cutover)
	status := r.Status()
	require.Equal(t, uint64(2), status.ObservedGeneration)
	require.Equal(t, uint64(1), status.AppliedGeneration)
	require.Equal(t, uint64(2), status.Customers["u1"].Revision)
	require.Equal(t, uint64(1), status.Customers["u2"].Revision)
	var requests sync.WaitGroup
	type reloadOutcome struct {
		revision uint64
		err      error
	}
	outcomes := make(chan reloadOutcome, 8)
	for i := range 8 {
		requests.Go(func() {
			revision := uint64(1 + i%2)
			_, err := r.ReloadCustomer(context.Background(), "u1", revision)
			outcomes <- reloadOutcome{revision, err}
		})
	}
	requests.Wait()
	close(outcomes)
	for outcome := range outcomes {
		if outcome.revision == 1 {
			require.Error(t, outcome.err)
		} else {
			require.NoError(t, outcome.err)
		}
	}
	require.Equal(t, uint64(2), r.Status().Customers["u1"].Revision)
	_, err = r.ReloadAll(context.Background(), 2)
	require.NoError(t, err)
	u1.Filters[0].ID = "same-revision-change"
	b.publish(t, 2, u1, u2)
	_, err = r.ReloadCustomer(context.Background(), "u1", 2)
	require.ErrorContains(t, err, "without advancing revision")
	require.NotEmpty(t, r.Status().ValidationError)
	u1.Filters[0].ID = "changed"
	b.publish(t, 2, u1, u2)
	_, err = r.ReloadAll(context.Background(), 2)
	require.NoError(t, err)
	_, err = r.ReloadAll(context.Background(), 3)
	require.Error(t, err)
	u1.Revision = 1
	b.publish(t, 3, u1, u2)
	_, err = r.ReloadAll(context.Background(), 3)
	require.ErrorContains(t, err, "stale customer")
	u1.Revision = 2
	u3 := localCustomer("u3")
	b.publish(t, 3, u1, u2, u3)
	_, err = r.ReloadCustomer(context.Background(), "u1", 2)
	require.ErrorContains(t, err, "roster")
	_, err = r.ReloadAll(context.Background(), 3)
	require.NoError(t, err)
	require.Len(t, r.Status().Customers, 3)
	b.publish(t, 4, u1, u3)
	_, err = r.ReloadCustomer(context.Background(), "u1", 2)
	require.ErrorContains(t, err, "roster")
	_, err = r.ReloadAll(context.Background(), 4)
	require.NoError(t, err)
	require.Len(t, r.Status().Customers, 2)
	_, err = r.ReloadCustomer(context.Background(), "u2", 1)
	require.ErrorContains(t, err, "does not exist")
}

func TestManagedFailedReplacementIsAtomic(t *testing.T) {
	b := newAcceptanceBundle(t)
	u1, u2 := localCustomer("u1"), localCustomer("u2")
	b.publish(t, 1, u1, u2)
	r := b.start(t, nil)
	u1.Revision = 2
	u1.Destinations[0].Options["path"] = "u1.directory"
	require.NoError(t, os.Mkdir(filepath.Join(b.options.LogDir, "u1.directory"), 0o700))
	u2.Revision = 2
	u2.Filters[0].EventType = event.TypeTransaction
	b.publish(t, 2, u1, u2)
	_, err := r.ReloadAll(context.Background(), 2)
	require.Error(t, err)
	status := r.Status()
	require.Equal(t, uint64(1), status.AppliedGeneration)
	require.Equal(t, uint64(1), status.Customers["u1"].Revision)
	require.Equal(t, uint64(1), status.Customers["u2"].Revision)
	r.dispatch(block("still-old"))
	require.NoError(t, r.Stop())
	for _, id := range []string{"u1", "u2"} {
		require.Equal(t, []string{"still-old"}, receiptLabels(t, filepath.Join(b.options.LogDir, id+".jsonl")))
	}
	require.Zero(t, r.Status().PluginInstances)
	require.Zero(t, r.Status().RetainedSize)
}

func TestManagedRemovalAndRotationRevokeQueuedOldDestination(t *testing.T) {
	for _, rotation := range []bool{false, true} {
		t.Run(fmt.Sprint("rotate=", rotation), func(t *testing.T) {
			received := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_, _ = io.Copy(io.Discard, req.Body)
				received <- req.URL.Path
				w.WriteHeader(204)
			}))
			defer server.Close()
			b := newAcceptanceBundle(t)
			u1, u2 := localCustomer("u1"), localCustomer("u2")
			u1.Destinations = append(u1.Destinations, b.webhook(t, "u1", "remote", server.URL+"/old", 1))
			b.publish(t, 1, u1, u2)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			r := b.start(t, func(r *Router) {
				r.matchGate = func(ctx context.Context, id string) {
					if id == "u1" {
						once.Do(func() { close(entered) })
						select {
						case <-release:
						case <-ctx.Done():
						}
					}
				}
			})
			r.dispatch(block("queued"))
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker not entered")
			}
			b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submitted_to_plugin", "u2") == 1 })
			u1.Revision = 2
			if rotation {
				u1.Destinations[1] = b.webhook(t, "u1", "remote", server.URL+"/new", 2)
			} else {
				u1.Destinations = u1.Destinations[:1]
			}
			b.publish(t, 2, u1, u2)
			_, err := r.ReloadCustomer(context.Background(), "u1", 2)
			require.NoError(t, err)
			close(release)
			r.dispatch(block("current"))
			require.NoError(t, r.Stop())
			var paths []string
			for len(received) > 0 {
				paths = append(paths, <-received)
			}
			if rotation {
				require.Equal(t, []string{"/new"}, paths)
			} else {
				require.Empty(t, paths)
			}
			require.Equal(t, uint64(1), r.Status().Counters.Canceled)
			require.Equal(t, uint64(1), r.Status().Counters.DestinationDropped)
			require.Equal(t, []string{"queued", "current"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u2.jsonl")))
		})
	}
}

func TestManagedStartupAndCredentialIsolation(t *testing.T) {
	for _, scenario := range []string{"missing customer", "wrong credential owner", "unsupported plugin", "foreign destination", "unsafe path", "missing credential version"} {
		t.Run(scenario, func(t *testing.T) {
			b := newAcceptanceBundle(t)
			c := localCustomer("u1")
			switch scenario {
			case "wrong credential owner", "missing credential version":
				d := b.webhook(t, "u2", "remote", "http://127.0.0.1:43210/private-secret-sentinel", 1)
				if scenario == "missing credential version" {
					d.Credentials["url"] = CredentialRef{File: "absent.json", Version: 2}
				}
				c.Destinations = []Destination{d}
			case "unsupported plugin":
				c.Destinations[0].Plugin = "slack"
			case "foreign destination":
				c.Filters[0].Destination = "u2/local"
			case "unsafe path":
				c.Destinations[0].Options["path"] = "../escaped.json"
			}
			b.publish(t, 1, c)
			if scenario == "missing customer" {
				require.NoError(t, os.Remove(filepath.Join(b.options.ConfigDir, "u1.json")))
			}
			r, err := New(b.options)
			require.NoError(t, err)
			err = r.StartContext(context.Background())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-secret-sentinel")
			require.False(t, r.Status().Ready)
			require.Zero(t, r.Status().LastDispatched)
			require.Nil(t, r.InputChan())
			require.NoError(t, r.Stop())
			require.Zero(t, r.Status().PluginInstances)
		})
	}
}

func TestManagedInstanceBudgetRejectsRepeatedReplacements(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.options.MaxInstances = 1
	c := localCustomer("u1")
	b.publish(t, 1, c)
	r := b.start(t, nil)
	c.Revision = 2
	c.Destinations[0].Options["path"] = "u1.replacement.jsonl"
	b.publish(t, 2, c)
	for range 10 {
		_, err := r.ReloadCustomer(context.Background(), "u1", 2)
		require.ErrorContains(t, err, "budget")
		require.Equal(t, int64(1), r.Status().PluginInstances)
		require.Equal(t, uint64(1), r.Status().Customers["u1"].Revision)
	}
	r.dispatch(block("retained"))
	require.NoError(t, r.Stop())
	require.Equal(t, []string{"retained"}, receiptLabels(t, filepath.Join(b.options.LogDir, "u1.jsonl")))
	require.Zero(t, r.Status().PluginInstances)
	require.Zero(t, r.Status().RetainedSize)
}

func TestManagedPrivateAPIAndRegistryDispositions(t *testing.T) {
	b := newAcceptanceBundle(t)
	b.publish(t, 1, localCustomer("opaque-customer"))
	r := b.start(t, nil)
	token := strings.Repeat("TOKEN_SENTINEL", 4)
	handler, err := r.Handler(token, func() string { return "disconnected" })
	require.NoError(t, err)
	for _, path := range []string{"/v1/config/status", "/v1/config/reload", "/v1/config/customers/opaque-customer/reload"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		require.Equal(t, http.StatusUnauthorized, out.Code)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	require.Equal(t, http.StatusNotFound, request(http.MethodGet, "/events", "").Code)
	status := request(http.MethodGet, "/v1/config/status", "")
	require.Equal(t, http.StatusOK, status.Code)
	require.Contains(t, status.Body.String(), `"source":"disconnected"`)
	require.NotContains(t, status.Body.String(), token)
	require.Equal(t, http.StatusOK, request(http.MethodGet, "/healthcheck", "").Code)
	require.Equal(t, http.StatusOK, request(http.MethodPost, "/v1/config/reload", `{"generation":1}`).Code)
	require.Equal(t, http.StatusConflict, request(http.MethodPost, "/v1/config/reload", `{"generation":1,"configuration":{"secret":"PAYLOAD_SENTINEL"}}`).Code)
	metrics := request(http.MethodGet, "/metrics", "")
	require.Equal(t, http.StatusOK, metrics.Code)
	require.NotContains(t, metrics.Body.String(), "opaque-customer")
	require.NotContains(t, metrics.Body.String(), token)
	caps := Capabilities()
	var registered, classified []string
	for _, entry := range plugin.GetPlugins(plugin.PluginTypeOutput) {
		registered = append(registered, entry.Name)
	}
	for _, capability := range caps {
		classified = append(classified, capability.Plugin)
	}
	slices.Sort(registered)
	slices.Sort(classified)
	require.Equal(t, registered, classified)
	require.Equal(t, []string{
		"clickhouse", "discord", "elasticsearch", "kafka", "log", "matrix",
		"mqtt", "nats", "notify", "notify-json", "postgres", "pubsub", "push",
		"rabbitmq", "redis", "s3", "signal", "slack", "sns", "sqs", "telegram", "webhook",
	}, registered)
	for _, capability := range caps {
		require.NotEmpty(t, capability.Reason)
		require.Equal(t, capability.Plugin == "log" || capability.Plugin == "webhook", capability.Enabled)
	}
	require.NotContains(t, b.diagnostics.text(), "PAYLOAD_SENTINEL")
	require.NotContains(t, b.diagnostics.text(), token)
}

func TestManagedAcceptThenDisconnectRemainsUnknown(t *testing.T) {
	accepted := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		accepted <- struct{}{}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	defer server.Close()
	b := newAcceptanceBundle(t)
	b.options.SendTimeout = 150 * time.Millisecond
	c := localCustomer("u1")
	c.Destinations = []Destination{b.webhook(t, "u1", "remote", server.URL, 1)}
	b.publish(t, 1, c)
	r := b.start(t, nil)
	r.dispatch(block("accepted-without-response"))
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not receive event")
	}
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submission_uncertain", "u1") == 1 })
	status := r.Status()
	require.Equal(t, uint64(1), status.Counters.SubmittedToPlugin)
	require.Equal(t, uint64(1), status.Counters.UnknownOutcomes)
	require.Equal(t, "degraded", status.Customers["u1"].Destinations["remote"].State)
	require.NoError(t, r.Stop())
	require.Empty(t, accepted, "retry must remain within the send budget")
}

func TestManagedRateLimitGroupDoesNotBlockAnotherOrigin(t *testing.T) {
	slowAttempts := make(chan struct{}, 8)
	fastReceipts := make(chan struct{}, 8)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		slowAttempts <- struct{}{}
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		fastReceipts <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer fast.Close()
	b := newAcceptanceBundle(t)
	b.options.SendTimeout = 200 * time.Millisecond
	var customers []Customer
	for _, id := range []string{"u1", "u2", "u3"} {
		c := localCustomer(id)
		target := slow.URL
		if id == "u3" {
			target = fast.URL
		}
		d := b.webhook(t, id, "remote", target, 1)
		if id != "u3" {
			d.IntervalMS = 1000
		}
		c.Destinations = []Destination{d}
		customers = append(customers, c)
	}
	b.publish(t, 1, customers...)
	r := b.start(t, nil)
	r.dispatch(block("quota-test"))
	select {
	case <-fastReceipts:
	case <-time.After(5 * time.Second):
		t.Fatal("unrelated limit group did not progress")
	}
	b.diagnostics.await(t, func(records []routeRecord) bool { return countAction(records, "submission_uncertain", "") == 2 })
	require.NoError(t, r.Stop())
	require.Len(t, slowAttempts, 1, "Retry-After must suppress the shared group's second customer and retries")
	require.Equal(t, uint64(3), r.Status().Counters.SubmittedToPlugin)
	require.Equal(t, uint64(3), r.Status().Counters.UnknownOutcomes)
}
