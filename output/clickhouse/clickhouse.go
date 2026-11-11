// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package clickhouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const (
	requestTimeout = 10 * time.Second
	flushInterval  = time.Second
	maxBatch       = 100
	createSQL      = `CREATE TABLE IF NOT EXISTS adder_events_v1 (
  schema_version UInt8,
  event_hash String,
  event_type LowCardinality(String),
  event_time DateTime64(9, 'UTC'),
  event_json String
) ENGINE = MergeTree
PARTITION BY toYYYYMM(event_time)
ORDER BY (event_time, event_type, event_hash)`
	insertSQL = `INSERT INTO adder_events_v1 FORMAT JSONEachRow`
)

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output writes bounded JSONEachRow batches to a ClickHouse MergeTree table.
type Output struct {
	plugin.Base
	endpoint, database, username, password string
	selected                               map[string]bool
	client                                 *http.Client
}
type Option func(*Output)

func WithURL(v string) Option                 { return func(o *Output) { o.endpoint = v } }
func WithDatabase(v string) Option            { return func(o *Output) { o.database = v } }
func WithUsername(v string) Option            { return func(o *Output) { o.username = v } }
func WithPassword(v string) Option            { return func(o *Output) { o.password = v } }
func WithEventTypes(v map[string]bool) Option { return func(o *Output) { o.selected = v } }
func New(opts ...Option) *Output {
	selected := make(map[string]bool, len(eventNames))
	for _, typ := range eventNames {
		selected[typ] = true
	}
	o := &Output{endpoint: "http://127.0.0.1:8123", database: "default", username: "default", selected: selected}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
func (o *Output) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (o *Output) Start() error            { return o.StartContext(context.Background()) }
func (o *Output) StartContext(ctx context.Context) error {
	return o.StartRun(ctx, plugin.BaseConfig{HasInput: true}, o.start, plugin.ShutdownHooks{})
}

func (o *Output) start(ctx context.Context) error {
	if err := o.validate(); err != nil {
		return err
	}
	if o.client == nil {
		o.client = &http.Client{Timeout: requestTimeout}
	}
	setupCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := o.query(setupCtx, createSQL, nil, false); err != nil {
		return fmt.Errorf("create ClickHouse table: %w", err)
	}
	done, in := o.Done(), o.Input()
	o.Go(func() {
		pending := make([][]byte, 0, maxBatch)
		flush := func() {
			if len(pending) == 0 {
				return
			}
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
			defer cancel()
			var body bytes.Buffer
			for _, line := range pending {
				body.Write(line)
				body.WriteByte('\n')
			}
			if err := o.query(flushCtx, insertSQL, body.Bytes(), true); err != nil {
				o.reportError(fmt.Errorf("insert ClickHouse batch: %w", err))
			}
			pending = pending[:0]
		}
		defer func() {
			// Stop cancels the run before joining this worker. Drain accepted events
			// and give the final batch its own bounded flush window.
			for {
				select {
				case evt, ok := <-in:
					if !ok {
						goto finalFlush
					}
					if o.selected[evt.Type] {
						line, err := encodeRow(evt)
						if err != nil {
							o.reportError(err)
						} else {
							pending = append(pending, line)
							if len(pending) >= maxBatch {
								flush()
							}
						}
					}
				default:
					goto finalFlush
				}
			}
		finalFlush:
			flush()
		}()
		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				flush()
			case evt, ok := <-in:
				if !ok {
					return
				}
				if !o.selected[evt.Type] {
					continue
				}
				line, err := encodeRow(evt)
				if err != nil {
					o.reportError(err)
					continue
				}
				pending = append(pending, line)
				if len(pending) >= maxBatch {
					flush()
				}
			}
		}
	})
	return nil
}

func (o *Output) validate() error {
	u, err := url.Parse(o.endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("url must be a ClickHouse http:// or https:// origin without credentials or path")
	}
	if o.username == "" {
		return errors.New("username is required")
	}
	if o.database == "" || strings.ContainsAny(o.database, " \t\r\n/\\") {
		return errors.New("database must name one ClickHouse database")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if o.password != "" && u.Scheme != "https" && !loopback {
		return errors.New("password requires https:// outside localhost")
	}
	return nil
}

func encodeRow(evt event.Event) ([]byte, error) {
	data, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("encode ClickHouse event: %w", err)
	}
	sum := sha256.Sum256(data)
	row := struct {
		SchemaVersion int    `json:"schema_version"`
		EventHash     string `json:"event_hash"`
		EventType     string `json:"event_type"`
		EventTime     string `json:"event_time"`
		EventJSON     string `json:"event_json"`
	}{1, hex.EncodeToString(sum[:]), evt.Type, evt.Timestamp.UTC().Format("2006-01-02 15:04:05.000000000"), string(data)}
	return json.Marshal(row)
}

func (o *Output) query(ctx context.Context, sql string, data []byte, insert bool) error {
	params := url.Values{"query": {sql}, "wait_end_of_query": {"1"}, "deduplicate_insert": {"disable"}}
	if insert {
		params.Set("insert_deduplicate", "0")
		params.Set("async_insert", "0")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(o.endpoint, "/")+"/?"+params.Encode(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ClickHouse-Database", o.database)
	req.Header.Set("X-ClickHouse-User", o.username)
	if o.password != "" {
		req.Header.Set("X-ClickHouse-Key", o.password)
	}
	client := *o.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-ClickHouse-Exception-Code") != "" || len(body) != 0 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}
func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.clickhouse")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
