// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugins_test

import (
	"fmt"
	"maps"
	"slices"

	"github.com/blinklabs-io/adder/event"
)

func Example_eventTransformation() {
	type reading struct {
		Labels map[string]string
		Values []int
	}
	incoming := event.Event{Payload: reading{
		Labels: map[string]string{"source": "original"},
		Values: []int{1, 2},
	}}
	transformed := incoming
	payload := incoming.Payload.(reading)
	payload.Labels = maps.Clone(payload.Labels)
	payload.Values = slices.Clone(payload.Values)
	payload.Labels["source"] = "transformed"
	payload.Values[0] = 9
	transformed.Payload = payload
	fmt.Println(
		incoming.Payload.(reading).Labels["source"],
		incoming.Payload.(reading).Values,
	)
	fmt.Println(
		transformed.Payload.(reading).Labels["source"],
		transformed.Payload.(reading).Values,
	)
	// Output:
	// original [1 2]
	// transformed [9 2]
}
