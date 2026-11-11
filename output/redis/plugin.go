// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "redis",
		Description:        "append selected events to a Redis Stream",
		NewFromOptionsFunc: newFromOptions,
		Options: []plugin.PluginOption{
			{Name: "address", Type: plugin.PluginOptionTypeString, Description: "Redis TCP host:port", DefaultValue: "127.0.0.1:6379"},
			{Name: "username", Type: plugin.PluginOptionTypeString, Description: "optional Redis ACL username", DefaultValue: ""},
			{Name: "password", Type: plugin.PluginOptionTypeString, Description: "optional Redis password", DefaultValue: ""},
			{Name: "tls", Type: plugin.PluginOptionTypeBool, Description: "use TLS for the Redis connection", DefaultValue: false},
			{Name: "stream", Type: plugin.PluginOptionTypeString, Description: "Redis Stream key", DefaultValue: "adder.events"},
			{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
		},
	})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	if err := plugin.ValidateHostPort(v.String("address")); err != nil {
		return nil, fmt.Errorf("address: %w", err)
	}
	if v.String("stream") == "" {
		return nil, errors.New("stream is required")
	}
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Redis event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one supported type")
	}
	o := New(WithAddress(v.String("address")), WithCredentials(v.String("username"), v.String("password")), WithTLS(v.Bool("tls")), WithStream(v.String("stream")), WithEventTypes(selected))
	o.SetLogger(logging.GetLogger().With("plugin", "output.redis"))
	return o, nil
}
