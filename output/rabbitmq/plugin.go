// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0
package rabbitmq

import (
	"errors"
	"fmt"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	"strconv"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "rabbitmq", Description: "publish selected events with broker confirms", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "url", Type: plugin.PluginOptionTypeString, Description: "AMQP broker URL", DefaultValue: "amqp://guest:guest@127.0.0.1:5672/"},
		{Name: "exchange", Type: plugin.PluginOptionTypeString, Description: "existing exchange; empty uses default exchange", DefaultValue: ""},
		{Name: "routing-key", Type: plugin.PluginOptionTypeString, Description: "routing key or queue name for default exchange", DefaultValue: "adder.events"},
		{Name: "persistent", Type: plugin.PluginOptionTypeString, Description: "publish persistent messages (true or false)", DefaultValue: "true"},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated selected event types", DefaultValue: "blocks,rollbacks,governance"},
	}})
}
func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	if err := validateURL(v.String("url")); err != nil {
		return nil, err
	}
	if v.String("routing-key") == "" {
		return nil, errors.New("routing-key is required")
	}
	persistent, err := strconv.ParseBool(v.String("persistent"))
	if err != nil {
		return nil, fmt.Errorf("persistent: %w", err)
	}
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported RabbitMQ event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	r := New(WithURL(v.String("url")), WithExchange(v.String("exchange")), WithRoutingKey(v.String("routing-key")), WithPersistent(persistent), WithEventTypes(selected))
	r.SetLogger(logging.GetLogger().With("plugin", "output.rabbitmq"))
	return r, nil
}
