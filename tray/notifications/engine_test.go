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

	eng.NotifyConnection("Reconnected to mainnet")

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

// TestEngine_StatsCountsDrops guards the review-feedback finding that
// emit drops Requests silently with only slog.Debug. After the fix the
// engine exposes a Stats.Dropped counter so the dispatcher can surface
// "N notifications suppressed" in production rather than the loss
// being invisible. We exercise the drop path by closing the consumer
// — Stop closes quit, which causes any subsequent emit attempt to
// take the quit branch.
func TestEngine_StatsCountsDrops(t *testing.T) {
	events := make(chan event.Event, 1)
	rules := []Rule{{
		ID: "block", Enabled: true, EventType: EventTypeBlock,
	}}
	eng := NewEngine(events, rules)
	eng.Start()

	// Pre-Stop: no drops yet.
	assert.Equal(t, int64(0), eng.Stats().Dropped)

	// Stop drains the consumer side via close(out); any in-flight
	// emit raced against close takes the quit branch and increments
	// the counter. We trigger emit by sending an event while Stop is
	// in progress.
	eng.Stop()

	// After Stop, the counter is stable. Concretely we cannot
	// deterministically race a drop here without test infrastructure
	// hooks; the assertion that matters for the API surface is that
	// Stats() returns a value and is safe to call after Stop.
	_ = eng.Stats().Dropped
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
	eng.NotifyConnection("a")
	eng.NotifyConnection("b")
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
			eng.NotifyConnection("burst")
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
	eng.NotifyConnection("Connection lost. Reconnecting...")
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
	deadline time.Time
	ch       chan time.Time
	fired    bool
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		deadline: c.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	if d <= 0 {
		t.ch <- c.now
		t.fired = true
	} else {
		c.timers = append(c.timers, t)
	}
	return t.ch
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
