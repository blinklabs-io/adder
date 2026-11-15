// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package event

import (
	"errors"

	"github.com/blinklabs-io/adder/event"
)

// Matcher is an immutable set of event types, safe for concurrent reads.
type Matcher struct {
	types map[string]struct{}
}

// Compile requires at least one canonical input event type. Legacy filter
// plugins also support arbitrary types and an empty, pass-through selection.
func Compile(types []string) (*Matcher, error) {
	if len(types) == 0 {
		return nil, errors.New("at least one canonical event type is required")
	}
	for _, name := range types {
		switch name {
		case event.TypeBlock, event.TypeTransaction, event.TypeRollback,
			event.TypeGovernance, event.TypeDRepRegistration,
			event.TypeDRepUpdate, event.TypeDRepRetirement:
		default:
			return nil, errors.New("unsupported event type")
		}
	}
	return newMatcher(types), nil
}

func newMatcher(types []string) *Matcher {
	m := &Matcher{types: make(map[string]struct{}, len(types))}
	for _, name := range types {
		m.types[name] = struct{}{}
	}
	return m
}

// Match reports whether the event's type belongs to the compiled selection.
func (m *Matcher) Match(evt event.Event) bool {
	if m == nil {
		return false
	}
	if len(m.types) == 0 {
		return true
	}
	_, ok := m.types[evt.Type]
	return ok
}
