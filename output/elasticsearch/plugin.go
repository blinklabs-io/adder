// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package elasticsearch

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "elasticsearch", Description: "index selected events in Elasticsearch", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "url", Type: plugin.PluginOptionTypeString, Description: "Elasticsearch origin URL", DefaultValue: "http://127.0.0.1:9200"},
		{Name: "index", Type: plugin.PluginOptionTypeString, Description: "versioned index name", DefaultValue: "adder-events-v1"},
		{Name: "api-key", Type: plugin.PluginOptionTypeString, Description: "optional Elasticsearch API key", DefaultValue: ""},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,transactions,rollbacks,governance,drep-registration,drep-update,drep-retirement"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Elasticsearch event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	o := New(WithURL(v.String("url")), WithIndex(v.String("index")), WithAPIKey(v.String("api-key")), WithEventTypes(selected))
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.SetLogger(logging.GetLogger().With("plugin", "output.elasticsearch"))
	return o, nil
}
