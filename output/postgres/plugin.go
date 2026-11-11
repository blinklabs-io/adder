// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "postgres", Description: "persist events in PostgreSQL", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "database-url", Type: plugin.PluginOptionTypeString, Description: "PostgreSQL connection URL", DefaultValue: ""},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated selected event types", DefaultValue: "blocks,transactions,rollbacks,governance,drep-registration,drep-update,drep-retirement"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	if err := validateURL(v.String("database-url")); err != nil {
		return nil, err
	}
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported PostgreSQL event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	o := New(WithURL(v.String("database-url")), WithEventTypes(selected))
	o.SetLogger(logging.GetLogger().With("plugin", "output.postgres"))
	return o, nil
}
