// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0
package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	amqp "github.com/rabbitmq/amqp091-go"
)

var eventNames = map[string]string{"blocks": event.TypeBlock, "transactions": event.TypeTransaction, "rollbacks": event.TypeRollback, "governance": event.TypeGovernance, "drep-registration": event.TypeDRepRegistration, "drep-update": event.TypeDRepUpdate, "drep-retirement": event.TypeDRepRetirement}

// RabbitMQOutput publishes selected events with mandatory routing and publisher confirms.
type RabbitMQOutput struct {
	plugin.Base
	url, exchange, routingKey string
	persistent                bool
	selected                  map[string]bool
	mu                        sync.Mutex
	conn                      *amqp.Connection
	channel                   *amqp.Channel
	returns                   chan amqp.Return
	transportMu               sync.Mutex
	transport                 net.Conn
}
type boundedConn struct {
	net.Conn
	timeout time.Duration
}

func (c boundedConn) Write(p []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.Conn.Write(p)
}

type Option func(*RabbitMQOutput)

func WithURL(v string) Option        { return func(r *RabbitMQOutput) { r.url = v } }
func WithExchange(v string) Option   { return func(r *RabbitMQOutput) { r.exchange = v } }
func WithRoutingKey(v string) Option { return func(r *RabbitMQOutput) { r.routingKey = v } }

func WithPersistent(v bool) Option            { return func(r *RabbitMQOutput) { r.persistent = v } }
func WithEventTypes(v map[string]bool) Option { return func(r *RabbitMQOutput) { r.selected = v } }
func New(opts ...Option) *RabbitMQOutput {
	r := &RabbitMQOutput{url: "amqp://127.0.0.1:5672/", routingKey: "adder.events", persistent: true, selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true}}
	for _, opt := range opts {
		opt(r)
	}
	return r
}
func (r *RabbitMQOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (r *RabbitMQOutput) Start() error            { return r.StartContext(context.Background()) }
func (r *RabbitMQOutput) StartContext(ctx context.Context) error {
	return r.StartRun(ctx, plugin.BaseConfig{HasInput: true}, r.start, plugin.ShutdownHooks{BeforeWait: r.close})
}

func (r *RabbitMQOutput) start(ctx context.Context) error {
	if err := validateURL(r.url); err != nil {
		return err
	}
	if r.routingKey == "" {
		return errors.New("routing-key is required")
	}
	if err := r.connect(); err != nil {
		return err
	}
	done, in := r.Done(), r.Input()
	r.Go(func() {
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
				if r.selected[evt.Type] {
					if err := r.publish(ctx, evt); err != nil && ctx.Err() == nil {
						r.reportError(err)
					}
				}
			}
		}
	})
	return nil
}

func (r *RabbitMQOutput) Stop() error { return r.Shutdown(plugin.ShutdownHooks{BeforeWait: r.close}) }

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "amqp" && u.Scheme != "amqps") || u.Fragment != "" {
		return errors.New("url must be an amqp:// or amqps:// broker URL")
	}
	return nil
}

func (r *RabbitMQOutput) connect() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.channel != nil && !r.channel.IsClosed() {
		return nil
	}
	if r.conn != nil {
		_ = r.conn.CloseDeadline(time.Now().Add(time.Second))
		r.conn = nil
	}
	var raw net.Conn
	var setupTimer *time.Timer
	defer func() {
		if setupTimer != nil {
			setupTimer.Stop()
		}
	}()
	conn, err := amqp.DialConfig(r.url, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
		c, e := net.DialTimeout(network, addr, 5*time.Second)
		if e != nil {
			return nil, e
		}
		raw = c
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		r.transportMu.Lock()
		r.transport = c
		r.transportMu.Unlock()
		// The client clears handshake deadlines before channel.open and confirm.select.
		setupTimer = time.AfterFunc(10*time.Second, func() { _ = c.Close() })
		return boundedConn{c, 10 * time.Second}, nil
	}})
	if err != nil {
		return fmt.Errorf("connect RabbitMQ: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = raw.Close()
		return fmt.Errorf("open RabbitMQ channel: %w", err)
	}
	if err = ch.Confirm(false); err != nil {
		_ = raw.Close()
		return fmt.Errorf("enable RabbitMQ confirms: %w", err)
	}
	_ = raw.SetReadDeadline(time.Time{})
	r.conn = conn
	r.channel = ch
	r.returns = ch.NotifyReturn(make(chan amqp.Return, 1))
	return nil
}

func (r *RabbitMQOutput) close() error {
	r.transportMu.Lock()
	if r.transport != nil {
		_ = r.transport.Close()
		r.transport = nil
	}
	r.transportMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.channel = nil
	if r.conn != nil {
		_ = r.conn.CloseDeadline(time.Now().Add(time.Second))
		r.conn = nil
	}
	return nil
}

func (r *RabbitMQOutput) publish(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode RabbitMQ event: %w", err)
	}
	if err := r.connect(); err != nil {
		return err
	}
	r.mu.Lock()
	ch, returns := r.channel, r.returns
	r.mu.Unlock()
	mode := uint8(amqp.Transient)
	if r.persistent {
		mode = amqp.Persistent
	}
	// One outstanding publish per channel keeps returns and confirmations correlated.
	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, r.exchange, r.routingKey, true, false, amqp.Publishing{ContentType: "application/json", DeliveryMode: mode, MessageId: fmt.Sprintf("adder-%d", time.Now().UnixNano()), Body: data})
	if err != nil {
		_ = r.close()
		return fmt.Errorf("publish RabbitMQ event: %w", err)
	}
	if confirm == nil {
		_ = r.close()
		return errors.New("RabbitMQ confirm mode unavailable")
	}
	if err := awaitConfirm(ctx, confirm.Done(), confirm.Acked, returns, 10*time.Second); err != nil {
		_ = r.close()
		return err
	}
	return nil
}

func awaitConfirm(ctx context.Context, done <-chan struct{}, acked func() bool, returns <-chan amqp.Return, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case returned, ok := <-returns:
		if !ok {
			return errors.New("RabbitMQ return channel closed; delivery is ambiguous")
		}
		return fmt.Errorf("RabbitMQ unroutable: %s", returned.ReplyText)
	case <-done:
		select {
		case returned, ok := <-returns:
			if !ok {
				return errors.New("RabbitMQ return channel closed; delivery is ambiguous")
			}
			return fmt.Errorf("RabbitMQ unroutable: %s", returned.ReplyText)
		default:
		}
		if !acked() {
			return errors.New("RabbitMQ publisher nack")
		}
		return nil
	case <-timer.C:
		return errors.New("RabbitMQ publisher confirm timeout; delivery is ambiguous")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RabbitMQOutput) reportError(err error) {
	logger := r.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.rabbitmq")
	}
	logger.Error(err.Error())
	r.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*RabbitMQOutput)(nil)
