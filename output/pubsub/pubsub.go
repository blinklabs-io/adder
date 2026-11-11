// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	gcp "cloud.google.com/go/pubsub/v2"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

const (
	publishTimeout  = 10 * time.Second
	maxMessageBytes = 10_000_000
)

var eventNames = map[string]string{
	"blocks": event.TypeBlock, "transactions": event.TypeTransaction,
	"rollbacks": event.TypeRollback, "governance": event.TypeGovernance,
	"drep-registration": event.TypeDRepRegistration,
	"drep-update":       event.TypeDRepUpdate,
	"drep-retirement":   event.TypeDRepRetirement,
}

// Output publishes selected events to a Google Cloud Pub/Sub topic.
type Output struct {
	plugin.Base
	projectID, topicID string
	selected           map[string]bool
	client             *gcp.Client
	publisher          *gcp.Publisher
	publishFn          func(context.Context, *gcp.Message) (string, error)
	timeout            time.Duration
}

type Option func(*Output)

func WithProjectID(v string) Option { return func(o *Output) { o.projectID = v } }
func WithTopicID(v string) Option   { return func(o *Output) { o.topicID = v } }
func WithEventTypes(v map[string]bool) Option {
	return func(o *Output) {
		o.selected = make(map[string]bool, len(v))
		for typ, enabled := range v {
			o.selected[typ] = enabled
		}
	}
}

func New(opts ...Option) *Output {
	o := &Output{timeout: publishTimeout, selected: map[string]bool{event.TypeBlock: true, event.TypeRollback: true, event.TypeGovernance: true}}
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
	if err := o.validate(); err != nil {
		return err
	}
	if o.publishFn == nil {
		client, err := gcp.NewClient(ctx, o.projectID)
		if err != nil {
			return fmt.Errorf("create Pub/Sub client: %w", err)
		}
		o.client = client
		o.publisher = client.Publisher("projects/" + o.projectID + "/topics/" + o.topicID)
		o.publisher.PublishSettings.CountThreshold = 1
		o.publisher.PublishSettings.DelayThreshold = 0
		o.publisher.PublishSettings.Timeout = o.timeout
		o.publishFn = func(ctx context.Context, message *gcp.Message) (string, error) {
			return o.publisher.Publish(ctx, message).Get(ctx)
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
				if err := o.publish(ctx, evt); err != nil && ctx.Err() == nil {
					o.reportError(err)
				}
			}
		}
	})
	return nil
}

func (o *Output) validate() error {
	if o.projectID == "" || strings.ContainsAny(o.projectID, "/ \t\r\n") {
		return errors.New("project-id must identify one Google Cloud project")
	}
	id := o.topicID
	if len(id) < 3 || len(id) > 255 || !asciiLetter(id[0]) || strings.HasPrefix(strings.ToLower(id), "goog") {
		return errors.New("topic-id must follow Pub/Sub topic naming rules")
	}
	for i := 1; i < len(id); i++ {
		b := id[i]
		if !asciiLetter(b) && (b < '0' || b > '9') && !strings.ContainsRune("-_.~+%", rune(b)) {
			return errors.New("topic-id must follow Pub/Sub topic naming rules")
		}
	}
	return nil
}
func asciiLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

func (o *Output) publish(ctx context.Context, evt event.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("encode Pub/Sub event: %w", err)
	}
	if len(data) > maxMessageBytes {
		return errors.New("Pub/Sub event exceeds 10 MB message limit")
	}
	requestCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	_, err = o.publishFn(requestCtx, &gcp.Message{Data: data, Attributes: map[string]string{"event_type": evt.Type}})
	if err != nil {
		return fmt.Errorf("publish Pub/Sub event: %w", err)
	}
	return nil
}

func (o *Output) close() error {
	if o.publisher != nil {
		o.publisher.Stop()
		o.publisher = nil
	}
	if o.client != nil {
		err := o.client.Close()
		o.client = nil
		o.publishFn = nil
		return err
	}
	return nil
}
func (o *Output) Stop() error { return o.Shutdown(plugin.ShutdownHooks{AfterWait: o.close}) }
func (o *Output) reportError(err error) {
	logger := o.Logger()
	if logger == nil {
		logger = logging.GetLoggerForComponent("output.pubsub")
	}
	logger.Error(err.Error())
	o.TrySendError(err)
}

var _ plugin.ManagedPlugin = (*Output)(nil)
