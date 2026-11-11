// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package clickhouse

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "clickhouse", Description: "batch events into ClickHouse", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "url", Type: plugin.PluginOptionTypeString, Description: "ClickHouse HTTP origin", DefaultValue: "http://127.0.0.1:8123"},
		{Name: "database", Type: plugin.PluginOptionTypeString, Description: "ClickHouse database", DefaultValue: "default"},
		{Name: "username", Type: plugin.PluginOptionTypeString, Description: "ClickHouse user", DefaultValue: "default"},
		{Name: "password", Type: plugin.PluginOptionTypeString, Description: "optional ClickHouse password", DefaultValue: ""},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,transactions,rollbacks,governance,drep-registration,drep-update,drep-retirement"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported ClickHouse event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	o := New(WithURL(v.String("url")), WithDatabase(v.String("database")), WithUsername(v.String("username")), WithPassword(v.String("password")), WithEventTypes(selected))
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.SetLogger(logging.GetLogger().With("plugin", "output.clickhouse"))
	return o, nil
}
