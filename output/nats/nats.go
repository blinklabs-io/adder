// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	natsgo "github.com/nats-io/nats.go"
)

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// NATSOutput publishes selected events using Core NATS.
type NATSOutput struct {
	plugin.Base
	servers       []string
	token         string
	subjectPrefix string
	selected      map[string]bool
	connection    *natsgo.Conn
	closed        chan struct{}
}

func New(options ...Option) *NATSOutput {
	n := &NATSOutput{
		servers: []string{natsgo.DefaultURL}, subjectPrefix: "adder.events",
		selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true},
	}
	for _, option := range options {
		option(n)
	}
	return n
}

func selectedTypes(names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, errors.New("event-types must name at least one supported type")
	}
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported NATS event type %q", name)
		}
		selected[typ] = true
	}
	return selected, nil
}

func validateSubjectPrefix(prefix string) error {
	if prefix == "" || strings.ContainsAny(prefix, " \t\r\n*>/") || strings.HasPrefix(prefix, ".") || strings.HasSuffix(prefix, ".") || strings.Contains(prefix, "..") {
		return errors.New("subject-prefix must contain nonempty NATS subject tokens without wildcards")
	}
	return nil
}

func (n *NATSOutput) Role() plugin.PluginType { return plugin.PluginTypeOutput }
func (n *NATSOutput) Start() error            { return n.StartContext(context.Background()) }
func (n *NATSOutput) StartContext(ctx context.Context) error {
	return n.StartRun(ctx, plugin.BaseConfig{HasInput: true}, n.start, plugin.ShutdownHooks{BeforeWait: n.close})
}

func (n *NATSOutput) start(ctx context.Context) error {
	if len(n.servers) == 0 {
		return errors.New("servers must contain at least one NATS URL")
	}
	if err := validateSubjectPrefix(n.subjectPrefix); err != nil {
		return err
	}
	// The prior run has already joined its workers and callbacks. A failed
	// reconnect attempt must not try to close that run's old connection.
	n.connection = nil
	closed := make(chan struct{})
	n.closed = closed
	opts := []natsgo.Option{
		natsgo.Name("adder-output"), natsgo.Timeout(5 * time.Second),
		natsgo.ReconnectBufSize(-1), natsgo.MaxReconnects(-1),
		natsgo.ErrorHandler(func(_ *natsgo.Conn, _ *natsgo.Subscription, err error) {
			n.reportError(fmt.Errorf("NATS server: %w", err))
		}),
		natsgo.ClosedHandler(func(*natsgo.Conn) { close(closed) }),
	}
	if n.token != "" {
		opts = append(opts, natsgo.Token(n.token))
	}
	conn, err := natsgo.Connect(strings.Join(n.servers, ","), opts...)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	n.connection = conn
	done, in := n.Done(), n.Input()
	n.Go(func() {
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
				n.publish(evt)
			}
		}
	})
	return nil
}

func (n *NATSOutput) publish(evt event.Event) {
	if !n.selected[evt.Type] {
		return
	}
	data, err := json.Marshal(evt)
	if err != nil {
		n.reportError(fmt.Errorf("encode NATS event: %w", err))
		return
	}
	subject := n.subjectPrefix + "." + strings.TrimPrefix(evt.Type, "input.")
	if err := n.connection.Publish(subject, data); err != nil {
		n.reportError(fmt.Errorf("publish NATS event: %w", err))
		return
	}
	// A PONG confirms the server processed preceding PUB commands. It does not
	// acknowledge a subscriber or make Core NATS delivery durable.
	if err := n.connection.FlushTimeout(5 * time.Second); err != nil {
		n.reportError(fmt.Errorf("flush NATS event: %w", err))
	}
}

func (n *NATSOutput) close() error {
	if n.connection != nil {
		n.connection.Close()
		<-n.closed // all earlier asynchronous callbacks have completed
	}
	return nil
}
func (n *NATSOutput) Stop() error { return n.Shutdown(plugin.ShutdownHooks{BeforeWait: n.close}) }
func (n *NATSOutput) reportError(err error) {
	logger := n.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.nats")
	}
	logger.Error(err.Error())
	n.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*NATSOutput)(nil)
