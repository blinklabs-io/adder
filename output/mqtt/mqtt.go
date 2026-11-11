// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
)

const operationTimeout = 5 * time.Second

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output publishes selected events to an MQTT 5 broker.
type Output struct {
	plugin.Base
	broker, clientID, username, password, topicPrefix string
	qos                                               byte
	timeout                                           time.Duration
	publishTimeout                                    time.Duration
	selected                                          map[string]bool
	mu                                                sync.Mutex
	client                                            *autopaho.ConnectionManager
	cancel                                            context.CancelFunc
}

type boundedConn struct {
	net.Conn
	timeout time.Duration
	stop    func() bool
}

func (c *boundedConn) Write(data []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.Conn.Write(data)
}

func (c *boundedConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

func newBoundedConn(ctx context.Context, conn net.Conn, timeout time.Duration) net.Conn {
	bounded := &boundedConn{Conn: conn, timeout: timeout, stop: context.AfterFunc(ctx, func() { _ = conn.Close() })}
	return packets.NewThreadSafeConn(bounded)
}

type Option func(*Output)

func WithBroker(v string) Option   { return func(o *Output) { o.broker = v } }
func WithClientID(v string) Option { return func(o *Output) { o.clientID = v } }
func WithCredentials(user, password string) Option {
	return func(o *Output) { o.username, o.password = user, password }
}
func WithTopicPrefix(v string) Option { return func(o *Output) { o.topicPrefix = v } }
func WithQoS(v byte) Option           { return func(o *Output) { o.qos = v } }
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
		broker: "mqtt://127.0.0.1:1883", clientID: "adder-output",
		topicPrefix: "adder/events", qos: 1, timeout: operationTimeout, publishTimeout: operationTimeout,
		selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true},
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *Output) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (o *Output) Start() error            { return o.StartContext(context.Background()) }
func (o *Output) StartContext(ctx context.Context) error {
	return o.StartRun(ctx, plugin.BaseConfig{HasInput: true}, o.start, plugin.ShutdownHooks{BeforeWait: o.close})
}

func (o *Output) start(ctx context.Context) error {
	if err := validateBroker(o.broker); err != nil {
		return err
	}
	if err := validateCredentials(o.broker, o.username, o.password); err != nil {
		return err
	}
	if err := validateTopicPrefix(o.topicPrefix); err != nil {
		return err
	}
	if err := validateTopics(o.topicPrefix, o.selected); err != nil {
		return err
	}
	if o.qos > 2 {
		return errors.New("qos must be 0, 1, or 2")
	}
	if o.clientID == "" {
		return errors.New("client-id is required")
	}
	if err := o.connect(ctx); err != nil {
		return err
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
				if err := o.publish(ctx, evt); err != nil && ctx.Err() == nil {
					o.reportError(err)
				}
			}
		}
	})
	return nil
}

func (o *Output) connect(ctx context.Context) error {
	u, err := brokerURL(o.broker)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	cfg := autopaho.ClientConfig{
		ServerUrls: []*url.URL{u}, KeepAlive: 30,
		CleanStartOnInitialConnection: true,
		ConnectTimeout:                o.timeout,
		ReconnectBackoff:              autopaho.NewConstantBackoff(time.Second),
		ConnectUsername:               o.username, ConnectPassword: []byte(o.password),
		ClientConfig: paho.ClientConfig{ClientID: o.clientID},
		AttemptConnection: func(_ context.Context, _ autopaho.ClientConfig, target *url.URL) (net.Conn, error) {
			dialCtx, dialCancel := context.WithTimeout(runCtx, o.timeout)
			defer dialCancel()
			var conn net.Conn
			var err error
			if target.Scheme == "tls" {
				conn, err = (&tls.Dialer{}).DialContext(dialCtx, "tcp", target.Host)
			} else {
				conn, err = (&net.Dialer{}).DialContext(dialCtx, "tcp", target.Host)
			}
			if err != nil {
				return nil, err
			}
			return newBoundedConn(runCtx, conn, o.timeout), nil
		},
	}
	client, err := autopaho.NewConnection(runCtx, cfg)
	if err != nil {
		cancel()
		return fmt.Errorf("start MQTT connection: %w", err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, o.timeout)
	err = client.AwaitConnection(waitCtx)
	waitCancel()
	if err != nil {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), o.timeout)
		_ = client.Disconnect(shutdownCtx)
		shutdownCancel()
		return fmt.Errorf("connect MQTT: %w", err)
	}
	o.mu.Lock()
	o.client, o.cancel = client, cancel
	o.mu.Unlock()
	return nil
}

func brokerURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Port() == "" {
		port := "1883"
		if u.Scheme == "tls" {
			port = "8883"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	return u, nil
}

func (o *Output) publish(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode MQTT event: %w", err)
	}
	topic := o.topicPrefix + "/" + strings.TrimPrefix(evt.Type, "input.")
	requestCtx, cancel := context.WithTimeout(ctx, o.publishTimeout)
	defer cancel()
	o.mu.Lock()
	client := o.client
	o.mu.Unlock()
	if client == nil {
		return errors.New("MQTT output is stopped")
	}
	if err := client.AwaitConnection(requestCtx); err != nil {
		return fmt.Errorf("wait for MQTT connection: %w", err)
	}
	resp, err := client.Publish(requestCtx, &paho.Publish{Topic: topic, QoS: o.qos, Retain: false, Payload: data})
	if err != nil {
		return fmt.Errorf("publish MQTT event (delivery uncertain): %w", err)
	}
	if resp == nil {
		return errors.New("MQTT broker returned no publish response")
	}
	if o.qos == 2 && resp.ReasonCode == 0x92 {
		return errors.New("MQTT QoS 2 completion uncertain: packet identifier not found")
	}
	if resp.ReasonCode >= 0x80 {
		return fmt.Errorf("MQTT broker rejected publication: reason code 0x%02x", resp.ReasonCode)
	}
	return nil
}

func (o *Output) close() error {
	o.mu.Lock()
	client, cancel := o.client, o.cancel
	o.client, o.cancel = nil, nil
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		ctx, done := context.WithTimeout(context.Background(), operationTimeout)
		defer done()
		return client.Disconnect(ctx)
	}
	return nil
}
func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{BeforeWait: o.close}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.mqtt")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

func validateBroker(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "mqtt" && u.Scheme != "tls") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("broker must be a mqtt:// or tls:// URL without credentials or path")
	}
	if u.Port() != "" || strings.HasSuffix(u.Host, ":") {
		if err := plugin.ValidateHostPort(u.Host); err != nil {
			return fmt.Errorf("invalid MQTT broker port: %w", err)
		}
	}
	return nil
}

func validateCredentials(broker, username, password string) error {
	if (username != "" || password != "") && strings.HasPrefix(broker, "mqtt://") {
		return errors.New("MQTT credentials require a tls:// broker")
	}
	return nil
}

func validateTopicPrefix(prefix string) error {
	if prefix == "" || !utf8.ValidString(prefix) || strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") || strings.Contains(prefix, "//") || strings.ContainsAny(prefix, "+#\x00") {
		return errors.New("topic-prefix must contain nonempty MQTT topic levels without wildcards")
	}
	return nil
}

func validateTopics(prefix string, selected map[string]bool) error {
	for typ, enabled := range selected {
		if enabled && len(prefix)+1+len(strings.TrimPrefix(typ, "input.")) > 65535 {
			return errors.New("MQTT topic exceeds 65535 bytes")
		}
	}
	return nil
}

var _ plugin.ManagedPlugin = (*Output)(nil)
