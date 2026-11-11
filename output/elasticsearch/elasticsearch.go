// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package elasticsearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	indexMapping   = `{"mappings":{"dynamic":"strict","properties":{"schema_version":{"type":"integer"},"event_type":{"type":"keyword"},"event_time":{"type":"date"},"event":{"type":"object","enabled":false}}}}`
)

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output indexes selected events in an Elasticsearch index with a versioned mapping.
type Output struct {
	plugin.Base
	endpoint, index, apiKey string
	selected                map[string]bool
	client                  *http.Client
}
type Option func(*Output)

func WithURL(v string) Option                 { return func(o *Output) { o.endpoint = v } }
func WithIndex(v string) Option               { return func(o *Output) { o.index = v } }
func WithAPIKey(v string) Option              { return func(o *Output) { o.apiKey = v } }
func WithEventTypes(v map[string]bool) Option { return func(o *Output) { o.selected = v } }
func New(opts ...Option) *Output {
	selected := make(map[string]bool, len(eventNames))
	for _, typ := range eventNames {
		selected[typ] = true
	}
	o := &Output{endpoint: "http://127.0.0.1:9200", index: "adder-events-v1", selected: selected}
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
	indexPath := "/" + url.PathEscape(o.index)
	status, body, err := o.request(setupCtx, http.MethodPut, indexPath, []byte(indexMapping))
	if err != nil {
		return fmt.Errorf("create Elasticsearch index: %w", err)
	}
	exists := status == http.StatusBadRequest && bytes.Contains(body, []byte(`"resource_already_exists_exception"`))
	if status != http.StatusOK && status != http.StatusCreated && !exists {
		return fmt.Errorf("create Elasticsearch index: HTTP %d: %s", status, body)
	}
	if exists {
		if err := o.verifyMapping(setupCtx, indexPath); err != nil {
			return err
		}
	}
	done, in := o.Done(), o.Input()
	o.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case evt, ok := <-in:
				if !ok {
					return
				}
				if !o.selected[evt.Type] {
					continue
				}
				if err := o.indexEvent(ctx, evt); err != nil && ctx.Err() == nil {
					o.reportError(err)
				}
			}
		}
	})
	return nil
}

func (o *Output) verifyMapping(ctx context.Context, path string) error {
	status, body, err := o.request(ctx, http.MethodGet, path+"/_mapping", nil)
	if err != nil {
		return fmt.Errorf("read Elasticsearch mapping: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("read Elasticsearch mapping: HTTP %d: %s", status, body)
	}
	var indexes map[string]struct {
		Mappings struct {
			Dynamic    string `json:"dynamic"`
			Properties map[string]struct {
				Type    string `json:"type"`
				Enabled *bool  `json:"enabled"`
			} `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(body, &indexes); err != nil {
		return fmt.Errorf("decode Elasticsearch mapping: %w", err)
	}
	mapping, ok := indexes[o.index]
	if !ok || mapping.Mappings.Dynamic != "strict" || mapping.Mappings.Properties["schema_version"].Type != "integer" || mapping.Mappings.Properties["event_type"].Type != "keyword" || mapping.Mappings.Properties["event_time"].Type != "date" {
		return errors.New("existing Elasticsearch index has incompatible event mapping")
	}
	eventField, ok := mapping.Mappings.Properties["event"]
	if !ok || eventField.Type != "object" || eventField.Enabled == nil || *eventField.Enabled {
		return errors.New("existing Elasticsearch index must disable event payload indexing")
	}
	return nil
}

func (o *Output) validate() error {
	u, err := url.Parse(o.endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("url must be an http:// or https:// Elasticsearch origin without credentials or path")
	}
	if o.apiKey != "" && u.Scheme != "https" {
		return errors.New("api-key requires an https:// Elasticsearch URL")
	}
	if len(o.index) == 0 || len(o.index) > 255 || strings.ToLower(o.index) != o.index || strings.ContainsAny(o.index, `\/*?"<>| ,#:`) || strings.ContainsAny(o.index[:1], "-_.+") {
		return errors.New("index must be a valid lowercase Elasticsearch index name")
	}
	return nil
}

func document(evt event.Event) (string, []byte, error) {
	raw, err := json.Marshal(evt)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(raw)
	doc := struct {
		SchemaVersion int             `json:"schema_version"`
		EventType     string          `json:"event_type"`
		EventTime     time.Time       `json:"event_time"`
		Event         json.RawMessage `json:"event"`
	}{1, evt.Type, evt.Timestamp, raw}
	data, err := json.Marshal(doc)
	if err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(sum[:]), data, nil
}

func (o *Output) indexEvent(ctx context.Context, evt event.Event) error {
	id, data, err := document(evt)
	if err != nil {
		return fmt.Errorf("encode Elasticsearch event: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	status, body, err := o.request(requestCtx, http.MethodPut, "/"+url.PathEscape(o.index)+"/_doc/"+id, data)
	if err != nil {
		return fmt.Errorf("index Elasticsearch event: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("index Elasticsearch event: HTTP %d: %s", status, body)
	}
	return nil
}

func (o *Output) request(ctx context.Context, method, path string, data []byte) (int, []byte, error) {
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(o.endpoint, "/")+path, bytes.NewReader(data))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if o.apiKey != "" {
			req.Header.Set("Authorization", "ApiKey "+o.apiKey)
		}
		client := *o.client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := client.Do(req)
		status := 0
		var body []byte
		if resp != nil {
			status = resp.StatusCode
			body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
		}
		if err == nil && status != 429 && status != 502 && status != 503 && status != 504 {
			return status, body, nil
		}
		if attempt == 3 {
			if err != nil {
				return status, body, err
			}
			return status, body, nil
		}
		timer := time.NewTimer(time.Duration(100<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, nil, ctx.Err()
		case <-timer.C:
		}
	}
	return 0, nil, errors.New("elasticsearch retry limit reached")
}
func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.elasticsearch")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
