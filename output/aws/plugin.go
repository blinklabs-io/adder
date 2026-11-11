// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	common := []plugin.PluginOption{
		{Name: "region", Type: plugin.PluginOptionTypeString, Description: "AWS region", DefaultValue: ""},
		{Name: "group-id", Type: plugin.PluginOptionTypeString, Description: "message group ID (required for FIFO)", DefaultValue: ""},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
	}
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "sqs", Description: "send selected events to an SQS queue", NewFromOptionsFunc: newSQSFromOptions,
		Options: append(append([]plugin.PluginOption(nil), common...), plugin.PluginOption{Name: "queue-url", Type: plugin.PluginOptionTypeString, Description: "SQS queue URL", DefaultValue: ""}),
	})
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "sns", Description: "publish selected events to an SNS topic", NewFromOptionsFunc: newSNSFromOptions,
		Options: append(append([]plugin.PluginOption(nil), common...),
			plugin.PluginOption{Name: "topic-arn", Type: plugin.PluginOptionTypeString, Description: "SNS topic ARN", DefaultValue: ""},
			plugin.PluginOption{Name: "max-message-bytes", Type: plugin.PluginOptionTypeInt, Description: "configured SNS topic message size limit", DefaultValue: 262144}),
	})
}

func newSQSFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) { return fromOptions("sqs", v) }

func newSNSFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) { return fromOptions("sns", v) }

func fromOptions(kind string, v plugin.Options) (plugin.ManagedPlugin, error) {
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported AWS event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	opts := []Option{WithRegion(v.String("region")), WithGroupID(v.String("group-id")), WithEventTypes(selected)}
	if kind == "sqs" {
		opts = append(opts, WithQueueURL(v.String("queue-url")))
	} else {
		opts = append(opts, WithTopicARN(v.String("topic-arn")), WithMaxMessageBytes(v.Int("max-message-bytes")))
	}
	o := newOutput(kind, opts...)
	if err := o.validate(); err != nil {
		return nil, err
	}
	o.SetLogger(logging.GetLogger().With("plugin", "output."+kind))
	return o, nil
}
