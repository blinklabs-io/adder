// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package s3

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "s3", Description: "archive selected events as S3 objects", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "region", Type: plugin.PluginOptionTypeString, Description: "AWS region", DefaultValue: ""},
		{Name: "bucket", Type: plugin.PluginOptionTypeString, Description: "S3 bucket", DefaultValue: ""},
		{Name: "prefix", Type: plugin.PluginOptionTypeString, Description: "relative object key prefix", DefaultValue: "adder/events"},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated selected event types", DefaultValue: "blocks,transactions,rollbacks,governance,drep-registration,drep-update,drep-retirement"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported S3 event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	o := New(WithRegion(v.String("region")), WithBucket(v.String("bucket")), WithPrefix(v.String("prefix")), WithEventTypes(selected))
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.SetLogger(logging.GetLogger().With("plugin", "output.s3"))
	return o, nil
}
