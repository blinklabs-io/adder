// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package nats

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
)

func init() {
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeOutput, Name: "nats",
		Description:        "publish selected events to Core NATS subjects",
		NewFromOptionsFunc: newFromOptions,
		Options: []plugin.PluginOption{
			{Name: "servers", Type: plugin.PluginOptionTypeString, Description: "comma-separated NATS server URLs", DefaultValue: "nats://127.0.0.1:4222"},
			{Name: "token", Type: plugin.PluginOptionTypeString, Description: "optional NATS authentication token", DefaultValue: ""},
			{Name: "subject-prefix", Type: plugin.PluginOptionTypeString, Description: "subject prefix for event subjects", DefaultValue: "adder.events"},
			{Name: "event-types", Type: plugin.PluginOptionTypeString, Description: "comma-separated event types", DefaultValue: "blocks,rollbacks,governance"},
		},
	})
}

func newFromOptions(values plugin.Options) (plugin.ManagedPlugin, error) {
	servers := plugin.SplitAndTrim(values.String("servers"))
	if len(servers) == 0 {
		return nil, errors.New("servers must contain a NATS URL")
	}
	for i, raw := range servers {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("servers entry %d must be a nats:// or tls:// URL without credentials or path", i+1)
		}
	}
	prefix := values.String("subject-prefix")
	if err := validateSubjectPrefix(prefix); err != nil {
		return nil, err
	}
	types, err := selectedTypes(plugin.SplitAndTrim(values.String("event-types")))
	if err != nil {
		return nil, err
	}
	return New(WithServers(servers...), WithToken(values.String("token")), WithSubjectPrefix(prefix), WithEventTypes(types), WithLogger(logging.GetLogger().With("plugin", "output.nats"))), nil
}
