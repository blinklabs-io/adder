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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

// recordingNotifier is a fake Notifier that records the most recent
// notification under a mutex, so an asynchronous dispatcher goroutine
// can be observed safely from the test goroutine under -race.
type recordingNotifier struct {
	mu   sync.Mutex
	last *fyne.Notification
}

func (r *recordingNotifier) SendNotification(n *fyne.Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = n
}

func (r *recordingNotifier) lastSent() *fyne.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func TestDispatch_SendsNotification(t *testing.T) {
	app := test.NewApp()
	reqs := make(chan Request, 1)

	test.AssertNotificationSent(
		t,
		fyne.NewNotification("💸 Incoming Transaction", "Received 5 ADA at a"),
		func() {
			reqs <- Request{
				RuleID: "wallet-in-0",
				Title:  "💸 Incoming Transaction",
				Body:   "Received 5 ADA at a",
			}
			close(reqs)
			Dispatch(reqs, app, nil, nil)
		},
	)
}

func TestDispatch_EmptyTitleFallsBackToAdder(t *testing.T) {
	app := test.NewApp()
	reqs := make(chan Request, 1)

	test.AssertNotificationSent(
		t,
		fyne.NewNotification("Adder", "body"),
		func() {
			reqs <- Request{Body: "body"}
			close(reqs)
			Dispatch(reqs, app, nil, nil)
		},
	)
}

func TestDispatch_StopsWhenChannelClosed(t *testing.T) {
	app := test.NewApp()
	reqs := make(chan Request)
	done := make(chan struct{})
	go func() {
		Dispatch(reqs, app, nil, nil)
		close(done)
	}()
	close(reqs)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return after channel close")
	}
}

// TestDispatch_FromEngine wires the engine to the dispatcher end-to-end:
// a transaction event flows through a Watch Wallet rule, is rendered
// with the Cardano formatters, and is sent via the Notifier.
func TestDispatch_FromEngine(t *testing.T) {
	n := &recordingNotifier{}
	events := make(chan event.Event, 1)
	rules := []Rule{{
		ID:          "wallet-in-0",
		Enabled:     true,
		EventType:   EventTypeTransaction,
		Params:      []string{"addr1qxy0123456789wxyz"},
		NotifyTitle: "💸 Incoming Transaction",
		NotifyBody:  tmplTxReceived,
	}}
	eng := NewEngine(events, rules)
	eng.Start()
	go Dispatch(eng.Requests(), n, eng.CurrentEpoch, eng.RecordDrop)
	t.Cleanup(eng.Stop)

	events <- event.Event{
		Type: EventTypeTransaction,
		Payload: map[string]any{
			"outputs": []any{
				map[string]any{
					"address": "addr1qxy0123456789wxyz",
					"amount":  float64(500_000_000),
				},
			},
		},
	}

	require.Eventually(t, func() bool {
		got := n.lastSent()
		return got != nil &&
			got.Title == "💸 Incoming Transaction" &&
			got.Content == "Received 500 ADA at addr1qxy…wxyz."
	}, 2*time.Second, 10*time.Millisecond)
}

// TestDispatch_DropsStaleEpochRequests is the regression guard for the
// SetRules-vs-Dispatch race documented on Engine.SetRules: a Request
// rendered against the OLD rule set must not be delivered after
// SetRules has bumped the engine's epoch. Construct the race
// synthetically by feeding the dispatcher a Request with Epoch=0 while
// CurrentEpoch returns 1 — the dispatcher must drop it without ever
// calling SendNotification.
func TestDispatch_DropsStaleEpochRequests(t *testing.T) {
	n := &recordingNotifier{}
	reqs := make(chan Request, 2)
	currentEpoch := func() int64 { return 1 }

	reqs <- Request{
		RuleID: "stale",
		Title:  "stale",
		Body:   "should be dropped",
		Epoch:  0,
	}
	reqs <- Request{
		RuleID: "fresh",
		Title:  "fresh",
		Body:   "should land",
		Epoch:  1,
	}
	close(reqs)

	var drops atomic.Int64
	done := make(chan struct{})
	go func() {
		Dispatch(reqs, n, currentEpoch, func() { drops.Add(1) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return")
	}

	got := n.lastSent()
	require.NotNil(t, got, "fresh notification should have been sent")
	require.Equal(t, "fresh", got.Title,
		"only the fresh-epoch notification should reach the Notifier")
	require.Equal(t, int64(1), drops.Load(),
		"stale-epoch drop must increment the recordDrop counter so "+
			"the unified Stats().Dropped reflects dispatch-side losses")
}

// TestEngine_SetRulesBumpsEpochAndDispatcherDropsInFlightStale exercises
// the end-to-end path: an event fires under the old rule set, then
// SetRules is called before Dispatch consumes the buffered Request;
// CurrentEpoch reports the new value and any in-flight stale Request
// must be dropped rather than delivered. The buffer drain inside
// SetRules already catches what is sitting in e.out at swap time; this
// test pins the dispatch-side filter that catches Requests already
// pulled into Dispatch's local register.
func TestEngine_SetRulesBumpsEpochAndDispatcherDropsInFlightStale(
	t *testing.T,
) {
	n := &recordingNotifier{}
	events := make(chan event.Event, 1)
	eng := NewEngine(events, []Rule{{
		ID:          "before",
		Enabled:     true,
		EventType:   EventTypeTransaction,
		NotifyTitle: "before",
		NotifyBody:  "old",
	}})
	require.Equal(t, int64(0), eng.CurrentEpoch())
	// Synthesise the race: send a Request directly into Dispatch with
	// the pre-bump epoch, then bump and confirm the Notifier never
	// sees the stale Request.
	eng.SetRules([]Rule{{
		ID:          "after",
		Enabled:     true,
		EventType:   EventTypeTransaction,
		NotifyTitle: "after",
		NotifyBody:  "new",
	}})
	require.Equal(t, int64(1), eng.CurrentEpoch(),
		"SetRules must bump the epoch counter")

	reqs := make(chan Request, 1)
	reqs <- Request{
		RuleID: "before",
		Title:  "before",
		Body:   "old",
		Epoch:  0, // pre-bump
	}
	close(reqs)
	Dispatch(reqs, n, eng.CurrentEpoch, eng.RecordDrop)

	require.Nil(t, n.lastSent(),
		"pre-bump Request must not be delivered after SetRules")
	require.GreaterOrEqual(t, eng.Stats().Dropped, int64(1),
		"dispatch-side stale drop must be reflected in Stats().Dropped")
}
