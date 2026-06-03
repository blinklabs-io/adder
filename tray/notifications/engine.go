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
}

// Clock abstracts time so the rate limiter is deterministic in tests.
type Clock interface {
	Now() time.Time
	// After returns a channel that fires after d. A fake clock may fire
	// it when its time is advanced past the deadline.
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// engineState tracks the lifecycle phase of an Engine. The transitions
// are strictly stateNew -> stateRunning -> stateStopped or
// stateNew -> stateStopped (Stop without Start). All transitions happen
// under Engine.mu so Start and Stop cannot race on it.
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
	rules  []Rule
	// rulesByType indexes rules by EventType so process can walk only
	// rules that can match the incoming event instead of scanning the
	// whole rule set per event.
	rulesByType map[string][]int
	out         chan Request
	connCh      chan event.Event
	clock       Clock
	limit       int
	window      time.Duration
	quit        chan struct{}
	done        chan struct{}
	mu          sync.Mutex
	state       engineState

	// dropped counts notification Requests that emit silently discarded
	// (consumer slow or shut down). Exposed via Stats so the dispatcher
	// can surface "N notifications suppressed" in production rather
	// than the loss being visible only via slog.Debug.
	dropped atomic.Int64
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

// Option configures an Engine.
type Option func(*Engine)

// WithRateLimit caps emitted requests to limit per window. Matches
// beyond the cap within a window are coalesced into a single batched
// Request flushed when the window elapses. A non-positive limit disables
// rate limiting.
func WithRateLimit(limit int, window time.Duration) Option {
	return func(e *Engine) {
		e.limit = limit
		e.window = window
	}
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
// the output channel, which is closed once the engine stops.
//
// NewEngine pre-compiles each rule's NotifyTitle/NotifyBody templates
// and builds an EventType → rule-index map so the hot path (process)
// avoids template.Parse on every event and skips rules that can never
// match the incoming event type.
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

// ConnectionEvent builds the synthesized connection-status event carried
// through the rule pipeline. The message is exposed at payload.message
// for templating.
func ConnectionEvent(message string) event.Event {
	return event.Event{
		Type:      EventTypeConnection,
		Timestamp: time.Now(),
		Payload:   map[string]any{"message": message},
	}
}

// NotifyConnection routes a connection-status notification through the
// engine's rule pipeline. It is the injection point that lets the tray
// app feed StatusTracker transitions ("Reconnecting...", "Reconnected to
// mainnet") through the same engine without the engine stealing the
// StatusTracker's single observer. Safe to call after Stop and safe to
// call before Start — in both cases the event is dropped rather than
// blocking the caller, so a burst of pre-Start transitions or a stalled
// run loop cannot deadlock the StatusTracker observer.
func (e *Engine) NotifyConnection(message string) {
	select {
	case e.connCh <- ConnectionEvent(message):
	case <-e.quit:
	default:
		slog.Debug("connection notification dropped: connCh full")
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

// Stop signals the engine to shut down, waits for the goroutine to exit,
// and closes the Requests channel. It is safe to call multiple times and
// safe to call concurrently with Start. If the engine was never started
// Stop closes the Requests channel itself so consumers ranging over it
// still terminate.
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
		// Never reached stateRunning: no run() goroutine will ever close
		// e.out, so close it here.
		close(e.out)
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
	sent        int              // requests emitted in the window
	pending     int              // matches coalesced into the batch
	timer       <-chan time.Time // flush deadline, nil when unarmed
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
			return
		case evt, ok := <-e.events:
			if !ok {
				e.events = nil // stop selecting on a closed channel
				continue
			}
			e.process(evt, lim)
		case evt := <-e.connCh:
			e.process(evt, lim)
		case <-lim.timer:
			e.flush(lim)
		}
	}
}

// flush emits any coalesced matches as a single batched Request and opens
// a fresh window.
func (e *Engine) flush(lim *limiter) {
	if lim.pending > 0 {
		e.emit(Request{
			RuleID:  "batch",
			Title:   "Adder",
			Body:    "Multiple events occurred.",
			Batched: true,
			Count:   lim.pending,
		})
	}
	lim.pending = 0
	lim.sent = 0
	lim.windowStart = e.clock.Now()
	lim.timer = nil
}

// process evaluates one event against all rules, emitting (or batching)
// a Request per matching rule. Each rule fires at most once per event.
func (e *Engine) process(evt event.Event, lim *limiter) {
	// Tumbling window: if the current window has elapsed start a fresh
	// one so a stale counter never mislabels an isolated event as a
	// batch. The reset must run even when a flush timer is already
	// armed, otherwise a pending batch carries an expired window's
	// sent counter forward indefinitely (the timer fires eventually
	// under a real clock, but during the gap incoming events would be
	// wrongly coalesced).
	if e.limit > 0 &&
		e.clock.Now().Sub(lim.windowStart) >= e.window {
		lim.sent = 0
		lim.windowStart = e.clock.Now()
	}

	// Walk only the rules registered for this event's type; rules of
	// other types cannot match, so the linear scan over all rules is
	// avoided.
	for _, i := range e.rulesByType[evt.Type] {
		r := &e.rules[i]
		if !r.Enabled {
			continue
		}
		if !evalMatchExpr(r.MatchExpr, evt) {
			continue
		}
		// Connection alerts are rare and important; they bypass the rate
		// limiter so a flood of chain events can never swallow a "lost
		// connection" notification into a generic batch, and they do
		// not consume a slot in the limiter budget (otherwise a burst
		// of connection events would push subsequent chain events into
		// the coalesced batch).
		if evt.Type == EventTypeConnection {
			e.emit(Request{
				RuleID: r.ID,
				Title:  renderRule(r.titleTmpl, r.NotifyTitle, evt),
				Body:   renderRule(r.bodyTmpl, r.NotifyBody, evt),
				Count:  1,
			})
			continue
		}
		if e.limit > 0 && lim.sent >= e.limit {
			// Over the cap for this window: coalesce and arm the timer.
			lim.pending++
			if lim.timer == nil {
				elapsed := e.clock.Now().Sub(lim.windowStart)
				remaining := e.window - elapsed
				if remaining < 0 {
					remaining = 0
				}
				lim.timer = e.clock.After(remaining)
			}
			continue
		}
		e.emit(Request{
			RuleID: r.ID,
			Title:  renderRule(r.titleTmpl, r.NotifyTitle, evt),
			Body:   renderRule(r.bodyTmpl, r.NotifyBody, evt),
			Count:  1,
		})
		lim.sent++
	}
}

// emit sends a Request without ever blocking the run loop. The send is
// preferred (out has a generous buffer), but a stalled or slow consumer
// must not be able to stop the engine from processing new events — so
// the default case drops the request with a debug log, and the quit
// case lets shutdown interrupt cleanly. Every drop is counted in
// e.dropped so production can surface it via Stats().
func (e *Engine) emit(req Request) {
	select {
	case e.out <- req:
	case <-e.quit:
		e.dropped.Add(1)
		slog.Debug("notification request dropped on shutdown",
			"ruleID", req.RuleID)
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
	t, err := template.New("n").Parse(raw)
	if err != nil {
		return nil
	}
	return t
}

// renderRule expands a pre-compiled rule template against the event,
// falling back to the raw string if the template is nil (no
// interpolation needed) or if Execute errors so a bad template never
// silently drops a notification.
func renderRule(tmpl *template.Template, raw string,
	evt event.Event,
) string {
	if tmpl == nil {
		return raw
	}
	data := map[string]any{
		"payload": evt.Payload,
		"context": evt.Context,
		"type":    evt.Type,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return raw
	}
	return buf.String()
}
