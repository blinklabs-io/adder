// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "mqtt", Description: "publish selected events to MQTT topics", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "broker", Type: plugin.PluginOptionTypeString, Description: "MQTT 5 broker TCP or TLS URL", DefaultValue: "mqtt://127.0.0.1:1883"},
		{Name: "client-id", Type: plugin.PluginOptionTypeString, Description: "MQTT client identifier", DefaultValue: "adder-output"},
		{Name: "username", Type: plugin.PluginOptionTypeString, Description: "optional MQTT username", DefaultValue: ""},
		{Name: "password", Type: plugin.PluginOptionTypeString, Description: "optional MQTT password", DefaultValue: ""},
		{Name: "topic-prefix", Type: plugin.PluginOptionTypeString, Description: "prefix for event topics", DefaultValue: "adder/events"},
		{Name: "qos", Type: plugin.PluginOptionTypeInt, Description: "MQTT publish QoS (0, 1, or 2)", DefaultValue: 1},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	if err := validateBroker(v.String("broker")); err != nil {
		return nil, err
	}
	if err := validateTopicPrefix(v.String("topic-prefix")); err != nil {
		return nil, err
	}
	qos := v.Int("qos")
	if qos < 0 || qos > 2 {
		return nil, errors.New("qos must be 0, 1, or 2")
	}
	if v.String("client-id") == "" {
		return nil, errors.New("client-id is required")
	}
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported MQTT event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	if err := validateTopics(v.String("topic-prefix"), selected); err != nil {
		return nil, err
	}
	o := New(WithBroker(v.String("broker")), WithClientID(v.String("client-id")), WithCredentials(v.String("username"), v.String("password")), WithTopicPrefix(v.String("topic-prefix")), WithQoS(byte(qos)), WithEventTypes(selected))
	o.SetLogger(logging.GetLogger().With("plugin", "output.mqtt"))
	return o, nil
}
