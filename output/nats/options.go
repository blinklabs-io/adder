// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package nats

import "github.com/blinklabs-io/adder/plugin"

type Option func(*NATSOutput)

func WithServers(servers ...string) Option {
	return func(n *NATSOutput) { n.servers = append([]string(nil), servers...) }
}
func WithToken(token string) Option          { return func(n *NATSOutput) { n.token = token } }
func WithSubjectPrefix(prefix string) Option { return func(n *NATSOutput) { n.subjectPrefix = prefix } }

func WithEventTypes(types map[string]bool) Option {
	return func(n *NATSOutput) {
		n.selected = make(map[string]bool, len(types))
		for typ, enabled := range types {
			n.selected[typ] = enabled
		}
	}
}

func WithLogger(logger plugin.Logger) Option { return func(n *NATSOutput) { n.SetLogger(logger) } }
