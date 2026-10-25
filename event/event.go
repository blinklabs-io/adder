// Copyright 2023 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package event

import (
	"time"
)

// Event type constants
const (
	TypeBlock            = "input.block"
	TypeTransaction      = "input.transaction"
	TypeRollback         = "input.rollback"
	TypeGovernance       = "input.governance"
	TypeDRepRegistration = "input.drep-registration"
	TypeDRepUpdate       = "input.drep-update"
	TypeDRepRetirement   = "input.drep-retirement"
)

// Event is immutable after publication to a plugin or pipeline channel.
// Copying Event does not copy the maps, slices, or pointers held by Context and
// Payload. Producers relinquish mutation rights when publishing. A transformer
// must copy any referenced data it changes; consumers must treat it as read-only.
// Custom payload types retain their concrete Go type; no implicit JSON cloning
// or serialization is performed by the pipeline.
type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Context   any       `json:"context,omitempty"`
	Payload   any       `json:"payload"`
	Type      string    `json:"type"`
	// A filter that constructs a replacement event must carry this value from
	// its source if the replacement should retain the source's pipeline timing.
	pipelineIngressTime time.Time
}

// WithPipelineIngressTime returns a copy with the pipeline entry time set.
// Filters creating derived events can copy this time from their source event.
func (e Event) WithPipelineIngressTime(t time.Time) Event {
	e.pipelineIngressTime = t
	return e
}

// PipelineIngressTime returns the pipeline entry time, or zero if unavailable.
func (e Event) PipelineIngressTime() time.Time {
	return e.pipelineIngressTime
}

func New(
	eventType string,
	timestamp time.Time,
	context, payload any,
) Event {
	return Event{
		Type:      eventType,
		Timestamp: timestamp,
		Context:   context,
		Payload:   payload,
	}
}
