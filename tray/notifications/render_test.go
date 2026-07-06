// Copyright 2026 Blink Labs Software
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

package notifications

import (
	"testing"

	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/assert"
)

// render is a test-only convenience that bundles parseTmpl +
// renderRule into the single-call shape the table tests want. Mirrors
// the production hot path (parse once at NewEngine, execute per event)
// but collapsed into one call since the table cases pair a literal
// template body with a specific event.
func render(raw string, evt event.Event) string {
	return renderRule(parseTmpl(raw), raw, evt, nil)
}

// renderWithParams is render with a non-nil .params slice for the
// few templates that distinguish watched addresses from counterparties.
func renderWithParams(
	raw string, evt event.Event, params []string,
) string {
	return renderRule(parseTmpl(raw), raw, evt, params)
}

// txWireEvent builds a transaction event with the JSON-decoded shape
// (context carries transactionHash; payload carries outputs) that
// arrives over the WebSocket.
func txWireEvent(outputs []any) event.Event {
	return event.Event{
		Type:    EventTypeTransaction,
		Context: map[string]any{"transactionHash": "abc0123456789hash"},
		Payload: map[string]any{"outputs": outputs},
	}
}

func TestRenderTemplates_RealPhrasings(t *testing.T) {
	outputs := []any{
		map[string]any{
			"address": "addr1qxy0123456789wxyz",
			"amount":  float64(500_000_000),
		},
		map[string]any{
			"address": "addr1other",
			"amount":  float64(100_000_000),
		},
	}

	tests := []struct {
		name   string
		tmpl   string
		evt    event.Event
		params []string
		want   string
	}{
		{
			name:   "received: watched address picked from outputs",
			tmpl:   tmplTxReceived,
			evt:    txWireEvent(outputs),
			params: []string{"addr1qxy0123456789wxyz"},
			want:   "Received 500 ADA at addr1qxy…wxyz.",
		},
		{
			name:   "sent: counterparty address picked from outputs",
			tmpl:   tmplTxSent,
			evt:    txWireEvent(outputs),
			params: []string{"addr1qxy0123456789wxyz"},
			want:   "Sent 100 ADA to addr1other.",
		},
		{
			name:   "token transfer renders the watched side",
			tmpl:   tmplTxToken,
			evt:    txWireEvent(outputs),
			params: []string{"addr1qxy0123456789wxyz"},
			want:   "Token transfer at addr1qxy…wxyz.",
		},
		{
			name: "generic tx uses context hash",
			tmpl: tmplTxGeneric,
			evt:  txWireEvent(outputs),
			want: "Transaction abc01234…hash.",
		},
		{
			name: "block minted",
			tmpl: tmplBlockMinted,
			evt: event.Event{
				Type:    EventTypeBlock,
				Context: map[string]any{"blockNumber": float64(12345)},
				Payload: map[string]any{
					"blockHash": "84ee913d2d3aaaaabbbb255af401",
				},
			},
			want: "Block #12345 (84ee913d...255af401) minted.",
		},
		{
			// Regression: a block number > ~1e6 used to render as
			// scientific notation ("Block #1.3335e+07 minted.")
			// because text/template's %v formatter switches modes
			// for large float64s. The `int` helper forces plain
			// digits regardless of magnitude.
			name: "block minted large height no scientific",
			tmpl: tmplBlockMinted,
			evt: event.Event{
				Type: EventTypeBlock,
				Context: map[string]any{
					"blockNumber": float64(13_335_000),
				},
				Payload: map[string]any{
					"blockHash": "84ee913d2d3aaaaabbbb255af401",
				},
			},
			want: "Block #13335000 (84ee913d...255af401) minted.",
		},
		{
			name: "pool block uses issuerVkey",
			tmpl: tmplPoolBlock,
			evt: event.Event{
				Type:    EventTypeBlock,
				Context: map[string]any{"blockNumber": float64(12345)},
				Payload: map[string]any{
					"issuerVkey": "pool0123456789wxyz",
					"blockHash":  "84ee913d2d3aaaaabbbb255af401",
				},
			},
			want: "Pool pool0123…wxyz minted block #12345 " +
				"(84ee913d...255af401).",
		},
		{
			name: "drep vote",
			tmpl: tmplGovVote,
			evt: event.Event{
				Type: EventTypeGovernance,
				Payload: map[string]any{
					"votingProcedures": []any{
						map[string]any{
							"voterId":        "drep1abc0123456789wxyz",
							"vote":           "Yes",
							"govActionIndex": float64(42),
						},
					},
				},
			},
			want: "DRep drep1abc…wxyz voted Yes on proposal #42.",
		},
		{
			// Regression (#9): an event carrying several votes must render
			// the FOLLOWED DRep's vote, not whichever is first. The
			// followed DRep is second here; voteFor(.params) selects it.
			name:   "drep vote picks followed drep among many",
			tmpl:   tmplGovVote,
			params: []string{"drep1followedAAAAwxyz"},
			evt: event.Event{
				Type: EventTypeGovernance,
				Payload: map[string]any{
					"votingProcedures": []any{
						map[string]any{
							"voterId":        "drep1otherCCCCDDDDzzzz",
							"vote":           "No",
							"govActionIndex": float64(7),
						},
						map[string]any{
							"voterId":        "drep1followedAAAAwxyz",
							"vote":           "Yes",
							"govActionIndex": float64(42),
						},
					},
				},
			},
			want: "DRep drep1fol…wxyz voted Yes on proposal #42.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want,
				renderWithParams(tt.tmpl, tt.evt, tt.params))
		})
	}
}

// TestWalletNotificationPhrasings_WatchedVsCounterparty asserts the
// rendered notification text for each row of the documented
// watched-vs-counterparty table. The wallet templates use the `mine`
// / `other` / `mineAda` / `otherAda` helpers (see format.go) so a
// future change that re-routes templates back to outAddr/outAda would
// regress these phrasings.
//
// Table:
//
//	+------------------------------------------+-----------------------------------+
//	| Tx shape                                 | Notification                      |
//	+------------------------------------------+-----------------------------------+
//	| You receive 500 ADA; sender gets 100 ADA | Received 500 ADA at <your-addr>   |
//	| change                                   |                                   |
//	+------------------------------------------+-----------------------------------+
//	| You send 100 ADA; 500 ADA change back to | Sent 100 ADA to <recipient>       |
//	| you                                      |                                   |
//	+------------------------------------------+-----------------------------------+
//	| Token transfer at your address           | Token transfer at <your-addr>     |
//	+------------------------------------------+-----------------------------------+
func TestWalletNotificationPhrasings_WatchedVsCounterparty(t *testing.T) {
	const (
		myAddr    = "addr1qxy_me_0123456789"
		theirAddr = "addr1qxy_them_9876543210"
	)
	watched := []string{myAddr}

	// receiveAndChange: incoming tx that also has a small change
	// output going back to the sender. The sum we report MUST be the
	// 500 ADA landing on the watched address, not the 600 ADA total.
	receiveAndChange := txWireEvent([]any{
		map[string]any{
			"address": myAddr,
			"amount":  float64(500_000_000),
		},
		map[string]any{
			"address": theirAddr,
			"amount":  float64(100_000_000),
		},
	})

	// sendAndChange: outgoing tx where the wallet's own change
	// output is larger than the amount actually sent to the
	// recipient. The reported destination MUST be the recipient,
	// and the reported amount MUST be the 100 ADA that left the
	// wallet — not the 500 ADA change.
	sendAndChange := txWireEvent([]any{
		map[string]any{
			"address": myAddr,
			"amount":  float64(500_000_000),
		},
		map[string]any{
			"address": theirAddr,
			"amount":  float64(100_000_000),
		},
	})

	// tokenAtWatched: a token-bearing output addressed to the
	// watched wallet. The phrasing must surface the watched address
	// — not the first output if the change happens to come first.
	tokenAtWatched := txWireEvent([]any{
		map[string]any{
			"address": theirAddr,
			"amount":  float64(100_000_000),
		},
		map[string]any{
			"address": myAddr,
			"amount":  float64(2_000_000),
			"assets": []any{
				map[string]any{
					"policy":      "polA",
					"fingerprint": "asset1abc",
					"quantity":    uint64(1),
				},
			},
		},
	})

	cases := []struct {
		name string
		tmpl string
		evt  event.Event
		want string
	}{
		{
			name: "row 1: receive 500 ADA + sender change " +
				"=> Received 500 ADA at <your-addr>",
			tmpl: tmplTxReceived,
			evt:  receiveAndChange,
			want: "Received 500 ADA at addr1qxy…6789.",
		},
		{
			name: "row 2: send 100 ADA + 500 ADA change " +
				"=> Sent 100 ADA to <recipient>",
			tmpl: tmplTxSent,
			evt:  sendAndChange,
			want: "Sent 100 ADA to addr1qxy…3210.",
		},
		{
			name: "row 3: token transfer at watched address " +
				"=> Token transfer at <your-addr>",
			tmpl: tmplTxToken,
			evt:  tokenAtWatched,
			want: "Token transfer at addr1qxy…6789.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want,
				renderWithParams(c.tmpl, c.evt, watched))
		})
	}
}

// TestRenderTemplates_MissingFields ensures a missing payload field
// renders a sensible (empty-valued) phrasing rather than leaking the raw
// template braces. Empty outputs must not panic the index path.
func TestRenderTemplates_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		evt  event.Event
		want string
	}{
		{
			name: "received with no outputs",
			tmpl: tmplTxReceived,
			evt: event.Event{
				Type:    EventTypeTransaction,
				Payload: map[string]any{},
			},
			want: "Received  ADA at .",
		},
		{
			name: "drep vote with no votes",
			tmpl: tmplGovVote,
			evt: event.Event{
				Type:    EventTypeGovernance,
				Payload: map[string]any{},
			},
			want: "DRep  voted  on proposal #.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render(tt.tmpl, tt.evt)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "{{",
				"raw template braces leaked into output")
		})
	}
}
