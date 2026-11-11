// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	redisgo "github.com/redis/go-redis/v9"
)

const operationTimeout = 5 * time.Second

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output appends selected events to a Redis Stream.
type Output struct {
	plugin.Base
	address, username, password, stream string
	tls                                 bool
	selected                            map[string]bool
	client                              *redisgo.Client
	timeout                             time.Duration
}

type Option func(*Output)

func WithAddress(v string) Option { return func(o *Output) { o.address = v } }
func WithCredentials(username, password string) Option {
	return func(o *Output) { o.username, o.password = username, password }
}
func WithTLS(v bool) Option      { return func(o *Output) { o.tls = v } }
func WithStream(v string) Option { return func(o *Output) { o.stream = v } }
func WithEventTypes(v map[string]bool) Option {
	return func(o *Output) {
		o.selected = make(map[string]bool, len(v))
		for typ, enabled := range v {
			o.selected[typ] = enabled
		}
	}
}

func New(opts ...Option) *Output {
	o := &Output{
		address: "127.0.0.1:6379", stream: "adder.events",
		selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true},
		timeout:  operationTimeout,
	}
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
	if err := plugin.ValidateHostPort(o.address); err != nil {
		return fmt.Errorf("address: %w", err)
	}
	if o.stream == "" {
		return errors.New("stream is required")
	}
	if (o.username != "" || o.password != "") && !o.tls {
		return errors.New("tls must be enabled for Redis credentials")
	}
	options := &redisgo.Options{
		Addr: o.address, Username: o.username, Password: o.password,
		MaxRetries: -1, DialTimeout: o.timeout, ReadTimeout: o.timeout, WriteTimeout: o.timeout,
		ContextTimeoutEnabled: true,
	}
	if o.tls {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	o.client = redisgo.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if err := o.client.Ping(pingCtx).Err(); err != nil {
		return fmt.Errorf("connect Redis: %w", err)
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
				if o.selected[evt.Type] {
					if err := o.publish(ctx, evt); err != nil && ctx.Err() == nil {
						o.reportError(err)
					}
				}
			}
		}
	})
	return nil
}

func (o *Output) publish(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode Redis event: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	_, err = o.client.XAdd(requestCtx, &redisgo.XAddArgs{
		Stream: o.stream, ID: "*",
		Values: map[string]any{"type": evt.Type, "event": string(data)},
	}).Result()
	if err != nil {
		return fmt.Errorf("append Redis stream event: %w", err)
	}
	return nil
}

func (o *Output) close() error {
	if o.client == nil {
		return nil
	}
	err := o.client.Close()
	o.client = nil
	return err
}

func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{AfterWait: o.close}) }

func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.redis")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
