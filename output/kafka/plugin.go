// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package kafka

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "kafka",
		Description:        "publish selected events to a Kafka topic",
		NewFromOptionsFunc: newFromOptions,
		Options: []plugin.PluginOption{
			{Name: "brokers", Type: plugin.PluginOptionTypeString, Description: "comma-separated Kafka bootstrap host:port addresses", DefaultValue: "127.0.0.1:9092"},
			{Name: "topic", Type: plugin.PluginOptionTypeString, Description: "Kafka topic", DefaultValue: "adder.events"},
			{Name: "username", Type: plugin.PluginOptionTypeString, Description: "optional SASL PLAIN username", DefaultValue: ""},
			{Name: "password", Type: plugin.PluginOptionTypeString, Description: "optional SASL PLAIN password", DefaultValue: ""},
			{Name: "tls", Type: plugin.PluginOptionTypeBool, Description: "use TLS for broker connections", DefaultValue: false},
			{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
		},
	})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	brokers := plugin.SplitAndTrim(v.String("brokers"))
	username, password := v.String("username"), v.String("password")
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Kafka event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one supported type")
	}
	o := New(WithBrokers(brokers...), WithTopic(v.String("topic")), WithCredentials(username, password), WithTLS(v.Bool("tls")), WithEventTypes(selected))
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.SetLogger(logging.GetLogger().With("plugin", "output.kafka"))
	return o, nil
}
