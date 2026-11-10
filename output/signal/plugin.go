// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0
package signal

import (
	"errors"
	"fmt"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{Type: plugin.PluginTypeOutput, Name: "signal", Description: "send events through signal-cli JSON-RPC", NewFromOptionsFunc: newFromOptions, Options: []plugin.PluginOption{
		{Name: "endpoint", Type: plugin.PluginOptionTypeString, Description: "signal-cli daemon HTTP JSON-RPC URL", DefaultValue: "http://127.0.0.1:8080/api/v1/rpc"},
		{Name: "account", Type: plugin.PluginOptionTypeString, Description: "signal-cli account for multi-account daemon", DefaultValue: ""},
		{Name: "recipients", Type: plugin.PluginOptionTypeString, Description: "comma-separated Signal recipient numbers or IDs", DefaultValue: ""},
		{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated selected event types", DefaultValue: "blocks,rollbacks,governance"},
	}})
}

func newFromOptions(v plugin.Options) (plugin.ManagedPlugin, error) {
	if err := validateEndpoint(v.String("endpoint")); err != nil {
		return nil, err
	}
	recipients := plugin.SplitAndTrim(v.String("recipients"))
	if len(recipients) == 0 {
		return nil, errors.New("recipients must contain at least one Signal recipient")
	}
	selected := make(map[string]bool)
	for _, name := range plugin.SplitAndTrim(v.String("event-types")) {
		typ, ok := eventNames[name]
		if !ok {
			return nil, fmt.Errorf("unsupported Signal event type %q", name)
		}
		selected[typ] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("event-types must name at least one type")
	}
	s := New(WithEndpoint(v.String("endpoint")), WithAccount(v.String("account")), WithRecipients(recipients...), WithEventTypes(selected))
	s.SetLogger(logging.GetLogger().With("plugin", "output.signal"))
	return s, nil
}
