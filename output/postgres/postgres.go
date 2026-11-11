// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	requestTimeout = 10 * time.Second
	schemaSQL      = `CREATE TABLE IF NOT EXISTS adder_events (
  arrival_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_hash text NOT NULL,
  schema_version smallint NOT NULL CHECK (schema_version = 1),
  event_type text NOT NULL,
  event_time timestamptz NOT NULL,
  event_json jsonb NOT NULL
)`
	indexSQL  = `CREATE INDEX IF NOT EXISTS adder_events_time_idx ON adder_events (event_time DESC)`
	insertSQL = `INSERT INTO adder_events (event_hash, schema_version, event_type, event_time, event_json)
VALUES ($1, 1, $2, $3, $4::jsonb)`
)

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output persists events in PostgreSQL as append-only, versioned JSONB rows.
type Output struct {
	plugin.Base
	url      string
	selected map[string]bool
	db       *sql.DB
}

type Option func(*Output)

func WithURL(v string) Option                 { return func(o *Output) { o.url = v } }
func WithEventTypes(v map[string]bool) Option { return func(o *Output) { o.selected = v } }
func New(opts ...Option) *Output {
	selected := make(map[string]bool, len(eventNames))
	for _, typ := range eventNames {
		selected[typ] = true
	}
	o := &Output{selected: selected}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *Output) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (o *Output) Start() error            { return o.StartContext(context.Background()) }
func (o *Output) StartContext(ctx context.Context) error {
	return o.StartRun(ctx, plugin.BaseConfig{HasInput: true}, o.start, plugin.ShutdownHooks{AfterWait: o.close})
}

func (o *Output) start(ctx context.Context) error {
	if err := validateURL(o.url); err != nil {
		return err
	}
	if o.db == nil {
		var err error
		o.db, err = sql.Open("pgx", o.url)
		if err != nil {
			return fmt.Errorf("open PostgreSQL: %w", err)
		}
		o.db.SetMaxOpenConns(2)
	}
	setupCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := o.db.PingContext(setupCtx); err != nil {
		return fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if _, err := o.db.ExecContext(setupCtx, schemaSQL); err != nil {
		return fmt.Errorf("create PostgreSQL schema: %w", err)
	}
	if _, err := o.db.ExecContext(setupCtx, indexSQL); err != nil {
		return fmt.Errorf("create PostgreSQL index: %w", err)
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
				if err := o.persist(ctx, evt); err != nil && ctx.Err() == nil {
					o.reportError(err)
				}
			}
		}
	})
	return nil
}

func validateURL(raw string) error {
	if raw == "" {
		return errors.New("database-url is required")
	}
	config, err := pgx.ParseConfig(raw)
	if err != nil || config.Host == "" || config.Database == "" {
		return errors.New("database-url must be a PostgreSQL connection string with host and database")
	}
	if config.Password != "" && !strings.HasPrefix(config.Host, "/") {
		if config.TLSConfig == nil {
			return errors.New("database-url with a password requires TLS (sslmode=require or stronger)")
		}
		for _, fallback := range config.Fallbacks {
			if fallback.TLSConfig == nil {
				return errors.New("database-url with a password must not permit a plaintext fallback")
			}
		}
	}
	return nil
}

func eventHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (o *Output) persist(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode PostgreSQL event: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := o.db.ExecContext(requestCtx, insertSQL, eventHash(data), evt.Type, evt.Timestamp, string(data)); err != nil {
		return fmt.Errorf("insert PostgreSQL event: %w", err)
	}
	return nil
}

func (o *Output) close() error {
	if o.db == nil {
		return nil
	}
	err := o.db.Close()
	o.db = nil
	return err
}
func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{AfterWait: o.close}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.postgres")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
