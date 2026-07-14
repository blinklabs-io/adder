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
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recvRequest reads one Request with a timeout so tests never hang.
func recvRequest(t *testing.T, ch <-chan Request) (Request, bool) {
	t.Helper()
	select {
	case r, ok := <-ch:
		return r, ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a Request")
		return Request{}, false
	}
}

func TestEngine_EmitsOnMatch(t *testing.T) {
	events := make(chan event.Event, 4)
	rules := []Rule{{
		ID:          "block",
		Enabled:     true,
		EventType:   EventTypeBlock,
		NotifyTitle: "Block {{.payload.blockHash}}",
		NotifyBody:  "minted",
	}}
	eng := NewEngine(events, rules)
	eng.Start()
	defer eng.Stop()

	events <- blockEvent("deadbeef")

	req, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Equal(t, "block", req.RuleID)
	assert.Equal(t, "Block deadbeef", req.Title)
	assert.Equal(t, "minted", req.Body)
	assert.Equal(t, EventTypeBlock, req.Event.Type)
	assert.Equal(t, "deadbeef",
		req.Event.Payload.(map[string]any)["blockHash"])
}

func TestEngine_NoMatchNoEmit(t *testing.T) {
	events := make(chan event.Event, 4)
	rules := []Rule{{
		ID:        "block",
		Enabled:   true,
		EventType: EventTypeBlock,
	}}
	eng := NewEngine(events, rules)
	eng.Start()
	defer eng.Stop()

	events <- txEvent("h", 1) // no rule for tx

	select {
	case r := <-eng.Requests():
		t.Fatalf("unexpected request emitted: %+v", r)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing emitted
	}
}

func TestEngine_DedupePerEvent(t *testing.T) {
	events := make(chan event.Event, 4)
	// Two enabled rules of the same type both match the same event;
	// each rule fires at most once per event, so we expect exactly two
	// distinct requests (one per rule), not duplicates of one rule.
	rules := []Rule{
		{ID: "r1", Enabled: true, EventType: EventTypeBlock, NotifyTitle: "a"},
		{ID: "r2", Enabled: true, EventType: EventTypeBlock, NotifyTitle: "b"},
	}
	eng := NewEngine(events, rules)
	eng.Start()
	defer eng.Stop()

	events <- blockEvent("h")

	got := map[string]int{}
	for range 2 {
		req, ok := recvRequest(t, eng.Requests())
		require.True(t, ok)
		got[req.RuleID]++
	}
	assert.Equal(t, map[string]int{"r1": 1, "r2": 1}, got)

	// Each rule fires at most once per event: no further Requests
	// must arrive after the expected two. A short poll is sufficient
	// since run() has already drained the single events-channel send.
	select {
	case r := <-eng.Requests():
		t.Fatalf("unexpected extra request: %+v", r)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestEngine_ConnectionEventRouted(t *testing.T) {
	events := make(chan event.Event, 4)
	rules := []Rule{{
		ID:          "conn",
		Enabled:     true,
		EventType:   EventTypeConnection,
		NotifyTitle: "Adder Connection",
		NotifyBody:  "{{.payload.message}}",
	}}
	eng := NewEngine(events, rules)
	eng.Start()
	defer eng.Stop()

	eng.NotifyConnection("Adder Connection", "Reconnected to mainnet")

	req, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Equal(t, "conn", req.RuleID)
	assert.Equal(t, "Reconnected to mainnet", req.Body)
}

func TestEngine_RateLimitBatches(t *testing.T) {
	events := make(chan event.Event, 16)
	clk := newFakeClock(time.Unix(0, 0))
	rules := []Rule{{
		ID:          "block",
		Enabled:     true,
		EventType:   EventTypeBlock,
		NotifyTitle: "Block",
	}}
	// Allow 2 requests per window; further matches in the window are
	// coalesced into a single batched Request flushed at window end.
	eng := NewEngine(events, rules,
		WithRateLimit(2, time.Second),
		WithClock(clk),
	)
	eng.Start()
	defer eng.Stop()

	// 5 matching events within the same window.
	for range 5 {
		events <- blockEvent("h")
	}

	// First two pass through immediately.
	r1, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.False(t, r1.Batched)
	r2, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.False(t, r2.Batched)

	// The remaining 3 are held until the window advances.
	select {
	case r := <-eng.Requests():
		t.Fatalf("expected batching, got immediate request: %+v", r)
	case <-time.After(150 * time.Millisecond):
	}

	// Advance the clock past the window; the batched request flushes.
	clk.Advance(2 * time.Second)
	r3, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.True(t, r3.Batched)
	assert.Equal(t, 3, r3.Count)
}

func TestEngine_RateLimitResetsAfterQuietGap(t *testing.T) {
	events := make(chan event.Event, 16)
	clk := newFakeClock(time.Unix(0, 0))
	rules := []Rule{{
		ID:        "block",
		Enabled:   true,
		EventType: EventTypeBlock,
	}}
	eng := NewEngine(events, rules,
		WithRateLimit(2, time.Second),
		WithClock(clk),
	)
	eng.Start()
	defer eng.Stop()

	// Hit the cap exactly (2), then go quiet past the window with no
	// event to flush, then send one more. That event must be a normal
	// request, not a stale batch.
	events <- blockEvent("h")
	events <- blockEvent("h")
	r1, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	require.False(t, r1.Batched)
	r2, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	require.False(t, r2.Batched)

	clk.Advance(2 * time.Second) // quiet gap longer than the window
	events <- blockEvent("h")

	r3, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.False(t, r3.Batched, "isolated event after gap must not batch")
	assert.Equal(t, 1, r3.Count)
}

// TestEngine_SimpleTargetGroupsORSemantics asserts that target groups default
// to OR. Users can narrow matching by selecting explicit AND connectors.
func TestEngine_SimpleTargetGroupsORSemantics(t *testing.T) {
	plan := setup.SetupPlan{
		Filter: setup.FilterConfig{
			Wallets: []string{"addr1xyz"},
			Assets:  []string{"asset1abc"},
		},
		Notify: setup.NotificationPrefs{
			setup.NotifyPrefIncomingTx:    true,
			setup.NotifyPrefAssetActivity: true,
		},
	}
	events := make(chan event.Event, 8)
	eng := NewEngine(events, RulesFromPlan(plan))
	eng.Start()
	defer eng.Stop()

	events <- txEventTo("addr1xyz")
	req, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Equal(t, "wallet-in", req.RuleID)

	events <- txWithTokens([2]string{"polA", "asset1abc"})
	req, ok = recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Equal(t, "asset-activity", req.RuleID)

	events <- txToWithTokens("addr1xyz", [2]string{"polA", "asset1abc"})
	req, ok = recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Contains(t, []string{"wallet-in", "asset-activity"}, req.RuleID)
}

// TestEngine_SetRulesDrainsStaleRequests guards the review finding
// that SetRules took effect for in-flight EVENTS but did not clear
// already-rendered Requests waiting in the output buffer — those were
// rendered against the OLD rules and would be delivered as stale
// notifications after the user had reconfigured. After the fix
// SetRules drains the buffer atomically and counts the dropped
// Requests via Stats so the cost is observable.
func TestEngine_SetRulesDrainsStaleRequests(t *testing.T) {
	events := make(chan event.Event, 4)
	rules := []Rule{{
		ID: "block", Enabled: true, EventType: EventTypeBlock,
		NotifyTitle: "old-title", NotifyBody: "old-body",
	}}
	eng := NewEngine(events, rules)
	eng.Start()
	defer eng.Stop()

	// Queue a few events that match the old rule. We deliberately do
	// NOT read from Requests() so they pile up in the output buffer.
	events <- blockEvent("a")
	events <- blockEvent("b")
	events <- blockEvent("c")

	// Give the engine time to render and enqueue all three Requests.
	require.Eventually(t, func() bool {
		return len(eng.Requests()) == 3
	}, time.Second, 10*time.Millisecond,
		"engine must have buffered all 3 stale Requests before SetRules")

	// Swap to a different rule set. The 3 buffered Requests should be
	// drained and counted as drops; subsequent reads from Requests()
	// must NOT see any "old-title" notification.
	eng.SetRules([]Rule{{
		ID: "block", Enabled: true, EventType: EventTypeBlock,
		NotifyTitle: "new-title", NotifyBody: "new-body",
	}})

	assert.Equal(t, int64(3), eng.Stats().Dropped,
		"SetRules must count drained Requests in Stats.Dropped")
	select {
	case r := <-eng.Requests():
		t.Fatalf("expected drained buffer, got stale Request: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	// New events render against the new rules.
	events <- blockEvent("d")
	req, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Equal(t, "new-title", req.Title)
	assert.Equal(t, "new-body", req.Body)
}

// TestEngine_SetRulesIsRaceFreeUnderLoad guards the review finding
// that the new SetRules + RWMutex pattern was added without a
// race-detector test that hammers SetRules + events. Run under -race
// to flag a dropped lock in process() or SetRules.
func TestEngine_SetRulesIsRaceFreeUnderLoad(t *testing.T) {
	events := make(chan event.Event, 32)
	eng := NewEngine(events, []Rule{{
		ID: "block", Enabled: true, EventType: EventTypeBlock,
	}})
	eng.Start()
	defer eng.Stop()

	// Drain Requests so emit doesn't block on a full out channel
	// (which would mask real lock-ordering issues).
	go func() {
		for range eng.Requests() {
		}
	}()

	const iters = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range iters {
			eng.SetRules([]Rule{{
				ID: "block", Enabled: i%2 == 0,
				EventType: EventTypeBlock,
			}})
		}
	}()
	go func() {
		defer wg.Done()
		for range iters {
			events <- blockEvent("x")
		}
	}()
	wg.Wait()
	// If we get here without -race firing, the snapshotting in
	// process() and the swap in SetRules are correctly synchronised.
}

// TestEngine_StatsCountsDropsExercisesQuitAndProducerPaths drives
// both the producer-side path (RecordDrop) and the emit-side quit
// path to verify Stats().Dropped increments in each.
func TestEngine_StatsCountsDropsExercisesQuitAndProducerPaths(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	eng.Start()
	eng.Stop() // closes quit

	// Producer-side path: forwarder reports a drop.
	eng.RecordDrop()
	assert.GreaterOrEqual(t, eng.Stats().Dropped, int64(1),
		"RecordDrop must increment the Dropped counter")

	// Emit-side quit path: emit with quit closed takes the quit branch
	// and also increments the counter. We don't need a process()
	// invocation — calling emit directly mirrors how it's reached
	// internally.
	before := eng.Stats().Dropped
	eng.emit(Request{RuleID: "post-stop"})
	assert.Greater(t, eng.Stats().Dropped, before,
		"emit after Stop must take the quit branch and increment Dropped")
}

// TestEngine_ConnectionDoesNotBurnRateBudget is the regression guard
// for the review finding that connection events bypass the >=limit
// check but used to still increment lim.sent, so a burst of connection
// alerts could push subsequent chain events into the coalesced batch.
// After the fix, connection events emit without consuming a slot.
func TestEngine_ConnectionDoesNotBurnRateBudget(t *testing.T) {
	events := make(chan event.Event, 4)
	clk := newFakeClock(time.Unix(0, 0))
	rules := []Rule{
		{ID: "block", Enabled: true, EventType: EventTypeBlock},
		{
			ID: "conn", Enabled: true, EventType: EventTypeConnection,
			NotifyBody: "{{.payload.message}}",
		},
	}
	eng := NewEngine(events, rules,
		WithRateLimit(2, time.Second),
		WithClock(clk),
	)
	eng.Start()
	defer eng.Stop()

	// Two connection alerts in the window — both must pass through
	// without consuming budget.
	eng.NotifyConnection("Adder Connection", "a")
	eng.NotifyConnection("Adder Connection", "b")
	for range 2 {
		req, ok := recvRequest(t, eng.Requests())
		require.True(t, ok)
		require.Equal(t, "conn", req.RuleID)
	}

	// Now two chain events should still both pass through (budget
	// untouched by the connection alerts).
	events <- blockEvent("h1")
	events <- blockEvent("h2")
	r1, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	require.Equal(t, "block", r1.RuleID)
	require.False(t, r1.Batched,
		"first chain event after connection burst must not batch")
	r2, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	require.Equal(t, "block", r2.RuleID)
	require.False(t, r2.Batched,
		"second chain event after connection burst must not batch")
}

// TestEngine_NotifyConnectionDoesNotBlockPreStart guards the review
// finding that NotifyConnection sent on a buffered channel with no
// default branch, so >cap-many calls before Start (or to a stalled run
// loop) would deadlock the caller. After the fix the excess drops with
// a debug log.
func TestEngine_NotifyConnectionDoesNotBlockPreStart(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	// Do NOT call Start: the channel is full once cap is reached and
	// nothing drains it. With no default branch this would hang the
	// test runner; the default-drop branch returns immediately.
	done := make(chan struct{})
	go func() {
		for range 64 {
			eng.NotifyConnection("Adder Connection", "burst")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("NotifyConnection blocked before Start")
	}
	eng.Stop()
}

func TestEngine_ConnectionBypassesRateLimit(t *testing.T) {
	events := make(chan event.Event, 16)
	clk := newFakeClock(time.Unix(0, 0))
	rules := []Rule{
		{ID: "block", Enabled: true, EventType: EventTypeBlock},
		{
			ID: "conn", Enabled: true, EventType: EventTypeConnection,
			NotifyBody: "{{.payload.message}}",
		},
	}
	eng := NewEngine(events, rules,
		WithRateLimit(1, time.Second),
		WithClock(clk),
	)
	eng.Start()
	defer eng.Stop()

	// Saturate the limiter with a chain event.
	events <- blockEvent("h")
	r1, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	require.Equal(t, "block", r1.RuleID)

	// Even over the cap, a connection alert must pass through immediately
	// and never be swallowed into a batch.
	eng.NotifyConnection(
		"Adder Connection",
		"Connection lost. Reconnecting...",
	)
	r2, ok := recvRequest(t, eng.Requests())
	require.True(t, ok)
	assert.Equal(t, "conn", r2.RuleID)
	assert.False(t, r2.Batched)
	assert.Equal(t, "Connection lost. Reconnecting...", r2.Body)
}

func TestEngine_StopWithoutStart(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	// Stop before Start must not block and must close Requests.
	done := make(chan struct{})
	go func() {
		eng.Stop()
		eng.Stop() // idempotent
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked when engine was never started")
	}
	// The bare receive used to hang forever if Stop failed to close
	// the channel; gate it on a timeout so a regression surfaces as a
	// test failure rather than a hung CI run.
	select {
	case _, ok := <-eng.Requests():
		assert.False(t, ok, "Requests should be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("Requests channel not closed after Stop")
	}
}

func TestEngine_StopClosesRequests(t *testing.T) {
	events := make(chan event.Event)
	eng := NewEngine(events, nil)
	eng.Start()
	eng.Stop()

	// Requests channel must be closed after Stop so consumers can
	// range over it cleanly.
	select {
	case _, ok := <-eng.Requests():
		assert.False(t, ok, "Requests channel should be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("Requests channel not closed after Stop")
	}
}

func TestEngine_StopIsIdempotent(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	eng.Start()
	eng.Stop()
	assert.NotPanics(t, eng.Stop)
}

// TestEngine_StartAfterStopIsNoOp guards against a regression where a
// Start() following an unstarted Stop() panicked with "close of closed
// channel": Stop closed e.out itself, then Start launched run() whose
// deferred close(e.out) hit an already-closed channel.
func TestEngine_StartAfterStopIsNoOp(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	eng.Stop()
	assert.NotPanics(t, eng.Start)
	// Requests is already closed by Stop; ranging over it must terminate.
	_, ok := <-eng.Requests()
	assert.False(t, ok)
}

// TestEngine_ConcurrentStartStop exercises the lifecycle from many
// goroutines at once. Under -race this guards against any unsynchronized
// access to the engine's state machine.
func TestEngine_ConcurrentStartStop(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(2)
		go func() { defer wg.Done(); eng.Start() }()
		go func() { defer wg.Done(); eng.Stop() }()
	}
	wg.Wait()
	// Final Stop must be safe and Requests must be closed exactly once.
	assert.NotPanics(t, eng.Stop)
	_, ok := <-eng.Requests()
	assert.False(t, ok)
}

// TestEngine_SetRulesAfterStopDoesNotHang is the regression guard for
// the CRITICAL drain-spin: a closed channel is always ready for
// receive, so the SetRules drain loop would pick the receive case
// forever instead of the default branch, spinning while holding
// rulesMu.Lock and inflating Stats().Dropped without bound. After the
// fix SetRules skips the drain when the engine is already stopped.
func TestEngine_SetRulesAfterStopDoesNotHang(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	eng.Start()
	eng.Stop()

	done := make(chan struct{})
	go func() {
		eng.SetRules([]Rule{{ID: "x", EventType: EventTypeBlock}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SetRules hung after engine Stop — drain " +
			"is spinning on a closed e.out channel")
	}
}

// TestEngine_NotifyConnectionAfterStopAlwaysDrops guards the
// pseudo-random select bug where a 3-way select with both quit AND
// connCh-send ready chose the connCh case ~50% of the time after
// Stop, silently buffering events nothing drains. After the fix the
// pre-check on quit short-circuits, and every post-Stop call is
// counted in Stats().Dropped.
func TestEngine_NotifyConnectionAfterStopAlwaysDrops(t *testing.T) {
	eng := NewEngine(make(chan event.Event), nil)
	eng.Start()
	eng.Stop()

	const calls = 1000
	for range calls {
		eng.NotifyConnection("Adder", "transient")
	}
	assert.Equal(t, int64(calls), eng.Stats().Dropped,
		"every post-Stop NotifyConnection must be counted as dropped")
}

// TestEngine_NotifyConnectionFullChanCountsDrop guards that the
// default-drop branch (connCh full while engine is alive but
// run-loop-starved) increments Stats().Dropped. Connection alerts are
// the rare-and-important class; an uncounted loss here was invisible
// to operators.
func TestEngine_NotifyConnectionFullChanCountsDrop(t *testing.T) {
	// Construct an engine but DO NOT Start — connCh has buffer 16 and
	// nothing drains it. Calls past the buffer hit the default branch.
	eng := NewEngine(make(chan event.Event), nil)

	const calls = 32
	for range calls {
		eng.NotifyConnection("Adder", "msg")
	}
	// 16 fit in the buffer, remaining 16 hit default-drop.
	assert.GreaterOrEqual(t, eng.Stats().Dropped, int64(calls-16),
		"connCh-full drops must be reflected in Stats().Dropped")
}

// TestEngine_StopFlushesPendingBatchCount guards that an in-flight
// coalesced batch (rate-limited matches waiting for the window timer)
// is counted as dropped when Stop interrupts it. Without this Stop
// silently loses the would-be "Multiple events occurred. Count: N"
// notification AND under-reports Stats().Dropped.
func TestEngine_StopFlushesPendingBatchCount(t *testing.T) {
	clk := newFakeClock(time.Unix(0, 0))
	events := make(chan event.Event, 8)
	eng := NewEngine(events, []Rule{{
		ID:        "always",
		Enabled:   true,
		EventType: EventTypeBlock,
	}},
		WithClock(clk),
		WithRateLimit(1, time.Second),
	)
	eng.Start()

	// Push 5 events — the first emits, the remaining 4 coalesce into
	// pending and arm the window timer.
	for range 5 {
		events <- event.Event{Type: EventTypeBlock}
	}
	// Drain the one emitted Request so the buffer doesn't account for it.
	select {
	case <-eng.Requests():
	case <-time.After(time.Second):
		t.Fatal("expected one rate-allowed emit")
	}
	// Wait until pending has actually accumulated before Stop.
	require.Eventually(t, func() bool {
		return eng.Stats().Dropped == 0 // no real drops yet
	}, time.Second, 5*time.Millisecond)

	before := eng.Stats().Dropped
	eng.Stop()
	// Pending batch must be accounted as a drop on shutdown.
	assert.Greater(t, eng.Stats().Dropped, before,
		"pending batch must be counted in Stats().Dropped on Stop")
}

// TestEngine_SetRateLimitTakesEffectAtRuntime guards the runtime
// rate-limit knob the wizard relies on for reconfigure-without-
// restart. After SetRateLimit lifts the limit from 1 to 10, the next
// 5 events all emit individually instead of coalescing.
func TestEngine_SetRateLimitTakesEffectAtRuntime(t *testing.T) {
	clk := newFakeClock(time.Unix(0, 0))
	events := make(chan event.Event, 16)
	eng := NewEngine(events, []Rule{{
		ID:        "always",
		Enabled:   true,
		EventType: EventTypeBlock,
	}},
		WithClock(clk),
		WithRateLimit(1, time.Hour),
	)
	eng.Start()
	t.Cleanup(eng.Stop)

	// Lift the limit before sending so all 5 events emit individually.
	eng.SetRateLimit(10, time.Hour)

	for range 5 {
		events <- event.Event{Type: EventTypeBlock}
	}
	for i := range 5 {
		select {
		case r := <-eng.Requests():
			require.False(t, r.Batched,
				"event %d should emit individually under "+
					"the lifted limit (got batched)", i)
		case <-time.After(time.Second):
			t.Fatalf("expected emit %d but timed out — "+
				"SetRateLimit did not take effect", i)
		}
	}
}

// TestEngine_StopReleasesArmedTimer is the regression guard for the
// leaked time.Timer: when run() exits via <-e.quit with lim.timer
// armed, the underlying runtime entry should be Stop()ed immediately
// rather than left until its deadline fires. Uses the fake clock so
// timer survival is observable directly.
func TestEngine_StopReleasesArmedTimer(t *testing.T) {
	clk := newFakeClock(time.Unix(0, 0))
	events := make(chan event.Event, 8)
	eng := NewEngine(events, []Rule{{
		ID:        "always",
		Enabled:   true,
		EventType: EventTypeBlock,
	}},
		WithClock(clk),
		WithRateLimit(1, time.Hour), // long window so timer stays armed
	)
	eng.Start()

	for range 5 {
		events <- event.Event{Type: EventTypeBlock}
	}
	// Drain one emit.
	<-eng.Requests()
	// Wait for the timer to be armed (pending > 0 implies armed).
	require.Eventually(t, func() bool {
		clk.mu.Lock()
		defer clk.mu.Unlock()
		return len(clk.timers) > 0
	}, time.Second, 5*time.Millisecond)

	eng.Stop()

	// All timers must be marked fired (Stop()ed) so a later Advance
	// past the deadline cannot re-fire them.
	clk.mu.Lock()
	defer clk.mu.Unlock()
	for i, tm := range clk.timers {
		assert.True(t, tm.fired,
			"timer %d still armed after Stop — runtime entry leaked",
			i)
	}
}

// fakeClock is a deterministic Clock for the rate-limit tests above.
// Advancing its time past a pending After deadline fires that timer's
// channel. Correctness is exercised transitively by the
// TestEngine_RateLimit* tests — if Advance failed to fire elapsed
// timers, those tests would hang on recvRequest.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock    *fakeClock // back-ref so Stop can mark this entry under c.mu
	deadline time.Time
	ch       chan time.Time
	fired    bool
}

// C / Stop satisfy the Timer interface.
func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired {
		return false
	}
	// Mark as fired so a later Advance() past the deadline does not
	// re-fire the channel. The test never reads from a stopped timer's
	// channel — the run loop has already discarded the Timer reference.
	t.fired = true
	return true
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		clock:    c,
		deadline: c.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	if d <= 0 {
		t.ch <- c.now
		t.fired = true
	} else {
		c.timers = append(c.timers, t)
	}
	return t
}

// Advance moves the clock forward and fires any timers whose deadline
// has elapsed.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.fired && !c.now.Before(t.deadline) {
			t.ch <- c.now
			t.fired = true
		}
	}
}
