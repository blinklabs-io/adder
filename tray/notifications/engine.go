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
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/blinklabs-io/adder/event"
)

// Request is an emitted notification request. The desktop dispatcher
// consumes these from Engine.Requests and performs the actual delivery.
type Request struct {
	// RuleID is the ID of the rule that produced this request. For a
	// batched request it is the synthetic ID "batch".
	RuleID string
	// Title is the rendered notification title.
	Title string
	// Body is the rendered notification body.
	Body string
	// Batched reports whether this request coalesces multiple matches
	// held back by the rate limiter.
	Batched bool
	// Count is the number of matches represented. It is 1 for a normal
	// request and >1 for a batched request.
	Count int
	// Epoch is the engine's rule-set generation when this Request was
	// rendered. Dispatch drops Requests with Epoch < CurrentEpoch to
	// avoid delivering notifications rendered against superseded
	// rules.
	Epoch int64
	// Event is the source event that matched the rule. It lets callers
	// build a history from the same filtered alert stream the desktop
	// dispatcher uses, instead of keeping a separate raw-event history.
	Event event.Event
}

// Timer is a stoppable single-shot timer mirroring *time.Timer so
// tests can inject a fake.
type Timer interface {
	// C fires once when the deadline elapses. Caller MUST guard the
	// select case (use limiter.timerCh) so a nil Timer does not panic.
	C() <-chan time.Time
	// Stop returns true if it stopped the timer before it fired.
	// Idempotent.
	Stop() bool
}

// Clock abstracts time so the rate limiter is deterministic in tests.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

// realTimer wraps *time.Timer to satisfy the Timer interface.
type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop() bool          { return r.t.Stop() }

// engineState tracks lifecycle phase. Transitions are strictly
// stateNew → stateRunning → stateStopped (or stateNew → stateStopped)
// and happen under Engine.mu.
type engineState uint8

const (
	stateNew engineState = iota
	stateRunning
	stateStopped
)

// Engine consumes events, evaluates them against a rule set, and emits
// notification Requests. It is fyne-free and does not import the tray
// package, so the desktop dispatcher can import it without a cycle.
type Engine struct {
	events <-chan event.Event
	// rulesMu protects the rule set so SetRules can swap it
	// concurrently with the run loop reading it. process() takes
	// RLock briefly to copy out the index for the incoming event type;
	// SetRules takes Lock to publish the new slice + index.
	rulesMu sync.RWMutex
	rules   []Rule
	// rulesByType indexes rules by EventType so process can walk only
	// rules that can match the incoming event instead of scanning the
	// whole rule set per event.
	rulesByType map[string][]int
	out         chan Request
	connCh      chan event.Event
	clock       Clock
	// limit / window (as nanoseconds) are atomics so SetRateLimit can
	// publish a new rate from any goroutine while process() reads it
	// per-event without a mutex on the hot path. A non-positive limit
	// disables coalescing.
	limit    atomic.Int64
	windowNs atomic.Int64
	quit     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	state    engineState

	// dropped counts Requests the engine produced but could not
	// deliver. Exposed via Stats so operators can surface
	// "N notifications suppressed".
	dropped atomic.Int64

	// epoch is the rule-set generation counter, bumped on every
	// SetRules. Dispatch drops Requests whose Epoch is older than
	// CurrentEpoch, closing the SetRules-vs-in-flight-Request race.
	epoch atomic.Int64
}

// CurrentEpoch returns the engine's current rule-set generation. Used
// by Dispatch to drop Requests rendered against a superseded rule set.
// Safe to call concurrently with SetRules and engine operation.
func (e *Engine) CurrentEpoch() int64 {
	return e.epoch.Load()
}

// Stats is a snapshot of operational counters useful for production
// observability. Returned by Engine.Stats.
type Stats struct {
	// Dropped is the total number of Requests the engine produced but
	// could not deliver — either because the output channel was full
	// (slow consumer) or because Stop had closed the quit channel.
	Dropped int64
}

// Stats returns a point-in-time snapshot of the engine's counters.
// Safe to call concurrently with engine operation.
func (e *Engine) Stats() Stats {
	return Stats{Dropped: e.dropped.Load()}
}

// RecordDrop increments the Dropped counter exposed via Stats so
// upstream producers (e.g. the tray's event forwarder) feed into the
// same unified counter as emit-side drops.
func (e *Engine) RecordDrop() {
	e.dropped.Add(1)
}

// Option configures an Engine.
type Option func(*Engine)

// WithRateLimit caps emitted requests to limit per window. Matches
// beyond the cap within a window are coalesced into a single batched
// Request flushed when the window elapses. A non-positive limit
// disables rate limiting. Equivalent to calling SetRateLimit after
// construction.
func WithRateLimit(limit int, window time.Duration) Option {
	return func(e *Engine) {
		e.SetRateLimit(limit, window)
	}
}

// SetRateLimit updates the limiter at runtime so a wizard reconfigure
// takes effect without restarting the engine. Safe to call from any
// goroutine; the new values are picked up on the next event the run
// loop processes (it does not retroactively reflow batches already in
// flight). A non-positive limit disables coalescing.
func (e *Engine) SetRateLimit(limit int, window time.Duration) {
	e.limit.Store(int64(limit))
	e.windowNs.Store(int64(window))
}

func (e *Engine) currentLimit() int {
	return int(e.limit.Load())
}

func (e *Engine) currentWindow() time.Duration {
	return time.Duration(e.windowNs.Load())
}

// WithClock injects a Clock (for deterministic tests). Defaults to a
// real wall-clock.
func WithClock(c Clock) Option {
	return func(e *Engine) {
		if c != nil {
			e.clock = c
		}
	}
}

// NewEngine creates an Engine reading from events and evaluating rules.
// Call Start to begin processing and Stop to shut down; Requests yields
// the output channel, which is closed once the engine stops. Templates
// are pre-compiled and an EventType→rule-index map is built so the hot
// path skips template parsing and irrelevant rules per event.
func NewEngine(
	events <-chan event.Event,
	rules []Rule,
	opts ...Option,
) *Engine {
	prepared := make([]Rule, len(rules))
	byType := make(map[string][]int, 4)
	for i, r := range rules {
		r.titleTmpl = parseTmpl(r.NotifyTitle)
		r.bodyTmpl = parseTmpl(r.NotifyBody)
		prepared[i] = r
		byType[r.EventType] = append(byType[r.EventType], i)
	}
	e := &Engine{
		events:      events,
		rules:       prepared,
		rulesByType: byType,
		out:         make(chan Request, 64),
		connCh:      make(chan event.Event, 16),
		clock:       realClock{},
		quit:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Requests returns the channel of emitted notification requests. It is
// closed after Stop completes.
func (e *Engine) Requests() <-chan Request { return e.out }

// SetRules atomically replaces the active rule set. Under rulesMu.Lock
// it publishes the new pre-compiled rules + EventType index, bumps the
// epoch, then drains buffered pre-swap Requests (counted in
// Stats().Dropped). In-flight Requests already pulled by Dispatch are
// dropped by the epoch comparison in Dispatch.
func (e *Engine) SetRules(rules []Rule) {
	prepared := make([]Rule, len(rules))
	byType := make(map[string][]int, 4)
	for i, r := range rules {
		r.titleTmpl = parseTmpl(r.NotifyTitle)
		r.bodyTmpl = parseTmpl(r.NotifyBody)
		prepared[i] = r
		byType[r.EventType] = append(byType[r.EventType], i)
	}
	e.rulesMu.Lock()
	e.rules = prepared
	e.rulesByType = byType
	e.epoch.Add(1)
	// Skip the drain on a stopped engine: e.out is closed, and a
	// closed channel is always ready for receive, so the drain loop
	// would spin forever picking the receive case over default.
	e.mu.Lock()
	stopped := e.state == stateStopped
	e.mu.Unlock()
	if stopped {
		e.rulesMu.Unlock()
		return
	}
	// Non-blocking drain under Lock so a concurrent process() cannot
	// enqueue a post-swap Request that we then accidentally drop.
	for {
		select {
		case <-e.out:
			e.dropped.Add(1)
		default:
			e.rulesMu.Unlock()
			return
		}
	}
}

// ConnectionEvent synthesizes the connection-status event that
// NotifyConnection feeds through the rule pipeline. Title and message
// are exposed at payload.title / payload.message for templating.
func ConnectionEvent(title, message string) event.Event {
	return event.Event{
		Type:      EventTypeConnection,
		Timestamp: time.Now(),
		Payload: map[string]any{
			"title":   title,
			"message": message,
		},
	}
}

// NotifyConnection feeds a connection-status event through the engine
// pipeline. Non-blocking — pre-Start or post-Stop calls drop into
// Stats().Dropped rather than stalling the caller.
func (e *Engine) NotifyConnection(title, message string) {
	// Two-phase select: the quit pre-check ensures a stopped engine
	// never buffers an event nothing drains (a 3-way select with both
	// quit and connCh-send ready would pick pseudo-randomly).
	select {
	case <-e.quit:
		e.dropped.Add(1)
		slog.Debug("connection notification dropped on shutdown",
			"title", title)
		return
	default:
	}
	select {
	case e.connCh <- ConnectionEvent(title, message):
	case <-e.quit:
		e.dropped.Add(1)
		slog.Debug("connection notification dropped on shutdown",
			"title", title)
	default:
		e.dropped.Add(1)
		slog.Warn(
			"connection notification dropped: connCh full",
			"title", title,
			"message", message)
	}
}

// Start launches the engine goroutine. It returns immediately and is
// safe to call multiple times concurrently with Stop; only the first
// call from the stateNew phase has effect. Once Stop has been called the
// engine cannot be restarted: subsequent Start calls are a no-op so
// run()'s deferred close(e.out) cannot panic on an already-closed
// channel.
func (e *Engine) Start() {
	e.mu.Lock()
	if e.state != stateNew {
		e.mu.Unlock()
		return
	}
	e.state = stateRunning
	e.mu.Unlock()
	go e.run()
}

// Stop signals shutdown, waits for the run goroutine to exit, and
// closes Requests. Idempotent and safe to call concurrently with
// Start. When called before Start, Stop also closes out and done so
// consumers don't hang.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.state == stateStopped {
		e.mu.Unlock()
		return
	}
	wasRunning := e.state == stateRunning
	e.state = stateStopped
	close(e.quit)
	if !wasRunning {
		// Never started: no run() goroutine will close these.
		close(e.out)
		close(e.done)
	}
	e.mu.Unlock()
	if wasRunning {
		<-e.done
	}
}

// limiter holds the rate limiter's tumbling-window state. It is owned by
// the single run() goroutine, so no mutex is needed.
type limiter struct {
	windowStart time.Time
	sent        int   // requests emitted in the window
	pending     int   // matches coalesced into the batch
	timer       Timer // flush deadline, nil when unarmed
}

// timerCh returns a typed-nil channel when no timer is armed so the
// run loop's select case is benignly blocked instead of panicking on
// a nil Timer.C().
func (l *limiter) timerCh() <-chan time.Time {
	if l.timer == nil {
		return nil
	}
	return l.timer.C()
}

// stopTimer releases the runtime timer (if armed) and clears the
// field. Idempotent.
func (l *limiter) stopTimer() {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
}

// run is the engine loop. All state it touches (the limiter and pending
// batch) is goroutine-local, so no mutex is needed.
func (e *Engine) run() {
	defer close(e.done)
	defer close(e.out)

	lim := &limiter{windowStart: e.clock.Now()}

	for {
		select {
		case <-e.quit:
			// Count an in-flight coalesced batch as dropped (its
			// timer can no longer fire) and release the timer entry.
			if lim.pending > 0 {
				e.dropped.Add(1)
				slog.Debug(
					"pending notification batch dropped on shutdown",
					"count", lim.pending)
			}
			lim.stopTimer()
			return
		case evt, ok := <-e.events:
			if !ok {
				e.events = nil // stop selecting on a closed channel
				continue
			}
			e.process(evt, lim)
		case evt := <-e.connCh:
			e.process(evt, lim)
		case <-lim.timerCh():
			e.flush(lim)
		}
	}
}

// flush emits any coalesced matches as a single batched Request and
// opens a fresh window. The batch carries the current epoch.
func (e *Engine) flush(lim *limiter) {
	if lim.pending > 0 {
		e.emit(Request{
			RuleID:  "batch",
			Title:   "Adder",
			Body:    "Multiple events occurred.",
			Batched: true,
			Count:   lim.pending,
			Epoch:   e.epoch.Load(),
		})
	}
	lim.pending = 0
	lim.sent = 0
	lim.windowStart = e.clock.Now()
	lim.stopTimer()
}

// process evaluates one event against all rules, emitting (or batching)
// a Request per matching rule. Each rule fires at most once per event.
//
// process holds rulesMu.RLock only long enough to snapshot rules,
// indices, and epoch — render and emit run without the lock. Each
// emitted Request is tagged with the snapshot's epoch so Dispatch
// drops Requests rendered against a superseded rule set.
func (e *Engine) process(evt event.Event, lim *limiter) {
	// Snapshot the rate-limit knobs once per event so a concurrent
	// SetRateLimit cannot tear the read across the two checks below.
	limit := e.currentLimit()
	window := e.currentWindow()

	// Tumbling window: when the current window has elapsed flush any
	// pending batch (so its timer cannot fire out-of-order after
	// later individual notifications) and reset the counter.
	if limit > 0 &&
		e.clock.Now().Sub(lim.windowStart) >= window {
		e.flush(lim)
		lim.sent = 0
		lim.windowStart = e.clock.Now()
	}

	e.rulesMu.RLock()
	rules := e.rules
	indices := e.rulesByType[evt.Type]
	epoch := e.epoch.Load()
	e.rulesMu.RUnlock()

	for _, i := range indices {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		// CustomMatch wins when set (asset/policy rules); otherwise
		// fall back to MatchExpr equality.
		if r.CustomMatch != nil {
			if !r.CustomMatch(evt) {
				continue
			}
		} else if !evalMatchExpr(r.MatchExpr, evt) {
			continue
		}
		// Connection alerts bypass the rate limiter so chain-event
		// floods cannot swallow a "lost connection" notification.
		if evt.Type == EventTypeConnection {
			e.emit(Request{
				RuleID: r.ID,
				Title:  renderRule(r.titleTmpl, r.NotifyTitle, evt, r.Params),
				Body:   renderRule(r.bodyTmpl, r.NotifyBody, evt, r.Params),
				Count:  1,
				Epoch:  epoch,
				Event:  evt,
			})
			continue
		}
		if limit > 0 && lim.sent >= limit {
			// Over the cap for this window: coalesce and arm the timer.
			lim.pending++
			if lim.timer == nil {
				elapsed := e.clock.Now().Sub(lim.windowStart)
				remaining := max(window-elapsed, 0)
				lim.timer = e.clock.NewTimer(remaining)
			}
			continue
		}
		e.emit(Request{
			RuleID: r.ID,
			Title:  renderRule(r.titleTmpl, r.NotifyTitle, evt, r.Params),
			Body:   renderRule(r.bodyTmpl, r.NotifyBody, evt, r.Params),
			Count:  1,
			Epoch:  epoch,
			Event:  evt,
		})
		lim.sent++
	}
}

// emit sends a Request without blocking the run loop: a full buffer or
// a closed quit channel drops the request and increments e.dropped.
// Must be called only from the run() goroutine — run() defers
// close(e.out), and a concurrent send on a closed channel panics
// regardless of select case. The pre-check on quit is defense-in-depth
// for any future off-goroutine refactor.
func (e *Engine) emit(req Request) {
	select {
	case <-e.quit:
		e.dropped.Add(1)
		slog.Debug("notification request dropped on shutdown",
			"ruleID", req.RuleID)
		return
	default:
	}
	select {
	case e.out <- req:
	default:
		e.dropped.Add(1)
		slog.Debug("notification request dropped: consumer slow",
			"ruleID", req.RuleID)
	}
}

// parseTmpl returns a pre-compiled template for raw, or nil if raw
// contains no `{{` or fails to parse. Called once per rule at engine
// construction so render does not re-parse on every event.
func parseTmpl(raw string) *template.Template {
	if !strings.Contains(raw, "{{") {
		return nil
	}
	t, err := template.New("n").Funcs(formatFuncs()).Parse(raw)
	if err != nil {
		return nil
	}
	return t
}

// renderRule expands a pre-compiled rule template against the event,
// falling back to the raw string if the template is nil or if Execute
// errors. The rule's Params slice is exposed at .params so templates
// can distinguish "the watched address" from the counterparty.
func renderRule(
	tmpl *template.Template, raw string,
	evt event.Event, params []string,
) string {
	if tmpl == nil {
		return raw
	}
	data := map[string]any{
		"payload": evt.Payload,
		"context": evt.Context,
		"type":    evt.Type,
		"params":  params,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return raw
	}
	return buf.String()
}
