// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "pubsub", Description: "publish selected events to Google Cloud Pub/Sub", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "project-id", Type: plugin.PluginOptionTypeString, Description: "Google Cloud project ID", DefaultValue: ""},
		{Name: "topic-id", Type: plugin.PluginOptionTypeString, Description: "Pub/Sub topic ID", DefaultValue: ""},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Pub/Sub event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	o := New(WithProjectID(v.String("project-id")), WithTopicID(v.String("topic-id")), WithEventTypes(selected))
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.SetLogger(logging.GetLogger().With("plugin", "output.pubsub"))
	return o, nil
}
