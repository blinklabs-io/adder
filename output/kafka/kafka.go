// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package kafka

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
)

const operationTimeout = 10 * time.Second

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output publishes selected events to one Kafka topic.
type Output struct {
	plugin.Base
	brokers            []string
	topic              string
	username, password string
	tls                bool
	selected           map[string]bool
	client             *kgo.Client
	timeout            time.Duration
}

type Option func(*Output)

func WithBrokers(v ...string) Option {
	return func(o *Output) { o.brokers = append([]string(nil), v...) }
}
func WithTopic(v string) Option { return func(o *Output) { o.topic = v } }
func WithCredentials(username, password string) Option {
	return func(o *Output) { o.username, o.password = username, password }
}
func WithTLS(v bool) Option { return func(o *Output) { o.tls = v } }
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
		brokers: []string{"127.0.0.1:9092"}, topic: "adder.events",
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

func validateBrokers(brokers []string) error {
	if len(brokers) == 0 {
		return errors.New("brokers must contain at least one host:port")
	}
	for i, broker := range brokers {
		if err := plugin.ValidateHostPort(broker); err != nil {
			return fmt.Errorf("brokers entry %d: %w", i+1, err)
		}
	}
	return nil
}

func validateTopic(topic string) error {
	if topic == "" || len(topic) > 249 || topic == "." || topic == ".." || strings.Trim(topic, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "" {
		return errors.New("topic must be 1-249 ASCII letters, digits, dots, underscores, or hyphens, excluding dot-only names")
	}
	return nil
}

func (o *Output) validate() error {
	if err := validateBrokers(o.brokers); err != nil {
		return err
	}
	if err := validateTopic(o.topic); err != nil {
		return err
	}
	if (o.username == "") != (o.password == "") {
		return errors.New("username and password must both be set for SASL PLAIN")
	}
	if o.username != "" && !o.tls {
		return errors.New("tls must be enabled for SASL PLAIN credentials")
	}
	return nil
}

func (o *Output) start(ctx context.Context) error {
	if err := o.validate(); err != nil {
		return err
	}
	opts := []kgo.Opt{
		kgo.WithContext(ctx), kgo.SeedBrokers(o.brokers...),
		kgo.DefaultProduceTopic(o.topic), kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.NoCompression()),
		kgo.MaxBufferedRecords(1), kgo.RecordDeliveryTimeout(o.timeout),
		kgo.AllowIdempotentProduceCancellation(),
	}
	if o.tls {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	if o.username != "" {
		opts = append(opts, kgo.SASL(plain.Auth{User: o.username, Pass: o.password}.AsMechanism()))
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("configure Kafka producer: %w", err)
	}
	o.client = client
	pingCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		return fmt.Errorf("connect Kafka: %w", err)
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
		return fmt.Errorf("encode Kafka event: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	_, err = o.client.ProduceSync(requestCtx, &kgo.Record{
		Key: []byte("adder.events"), Value: data,
	}).First()
	if err != nil {
		return fmt.Errorf("publish Kafka event (delivery uncertain): %w", err)
	}
	return nil
}

func (o *Output) close() error {
	if o.client != nil {
		o.client.Close()
		o.client = nil
	}
	return nil
}

func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{AfterWait: o.close}) }

func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.kafka")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
