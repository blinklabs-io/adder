// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

// Counters distinguish admission, rule matches, plugin submission and loss.
type Counters struct {
	InputDropped       uint64 `json:"input_dropped"`
	Matched            uint64 `json:"matched"`
	DestinationDropped uint64 `json:"destination_dropped"`
	SubmittedToPlugin  uint64 `json:"submitted_to_plugin"`
	PluginErrors       uint64 `json:"plugin_errors"`
	UnknownOutcomes    uint64 `json:"unknown_outcomes"`
	Canceled           uint64 `json:"canceled"`
	DiagnosticDropped  uint64 `json:"diagnostic_dropped"`
}

type counterSet struct{ inputDropped, matched, destinationDropped, submitted, pluginErrors, unknown, canceled, diagnosticDropped atomic.Uint64 }

func (c *counterSet) value() Counters {
	return Counters{
		InputDropped: c.inputDropped.Load(), Matched: c.matched.Load(),
		DestinationDropped: c.destinationDropped.Load(), SubmittedToPlugin: c.submitted.Load(),
		PluginErrors: c.pluginErrors.Load(), UnknownOutcomes: c.unknown.Load(),
		Canceled: c.canceled.Load(), DiagnosticDropped: c.diagnosticDropped.Load(),
	}
}

type item struct {
	event      event.Event
	occurrence uint64
	config     *appliedCustomer
	weight     int64
}
type submission struct {
	event                event.Event
	occurrence, revision uint64
	rules                []string
	weight               int64
}
type appliedCustomer struct {
	cutover      uint64
	prepared     *preparedCustomer
	destinations map[string]*destination
	refs         atomic.Int64
	router       *Router
	weight       int64
}

func (a *appliedCustomer) release() {
	if a.refs.Add(-1) == 0 {
		a.router.releaseMemory(a.weight)
		for _, d := range a.destinations {
			d.release()
		}
	}
}

type customerLane struct {
	retained atomic.Int64
	id       string
	input    chan item
	done     chan struct{}
	active   *appliedCustomer
	counters counterSet
	router   *Router
}

type diagnosticEntry struct {
	level   slog.Level
	message string
	attrs   []slog.Attr
}

// Router is an opt-in pipeline output. It owns nested output plugin instances.
type Router struct {
	revisionState
	capacity        atomic.Int64
	diagnosticMu    sync.Mutex
	diagnostics     chan diagnosticEntry
	diagnosticsDone chan struct{}

	stopping atomic.Bool
	plugin.Base
	options        Options
	mu             sync.Mutex
	reload         chan struct{}
	lanes          map[string]*customerLane
	observed       uint64
	applied        uint64
	manifestDigest [32]byte
	sequence       uint64
	validation     string
	ready          bool
	runCtx         context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	memory         atomic.Int64
	instances      atomic.Int64
	groupMu        sync.Mutex
	groups         map[string]*limitGroup
	counters       counterSet
	// matchGate is used only by deterministic package tests to suspend a lane.
	matchGate    func(context.Context, string)
	beforeSwitch func()
}

// New validates operator limits without reading configuration or starting workers.
func New(options Options) (*Router, error) {
	if err := options.defaults(); err != nil {
		return nil, err
	}
	options.AllowedHosts = append([]string(nil), options.AllowedHosts...)
	return &Router{options: options, reload: make(chan struct{}, 1)}, nil
}

// Role identifies the router as a pipeline output.
func (r *Router) Role() plugin.PluginType { return plugin.PluginTypeOutput }

// StartContext validates and applies the initial bundle before accepting events.
func (r *Router) StartContext(ctx context.Context) error {
	return r.StartRun(ctx, plugin.BaseConfig{HasInput: true, DrainOnStop: true}, func(ctx context.Context) error {
		if err := r.startDiagnostics(ctx); err != nil {
			return err
		}
		r.stopping.Store(false)
		r.mu.Lock()
		r.lanes = map[string]*customerLane{}
		r.groups = map[string]*limitGroup{}
		r.revisionState = revisionState{}
		r.observed = 0
		r.applied = 0
		r.sequence = 0
		r.validation = ""
		r.ready = false
		r.runCtx, r.cancel = context.WithCancel(context.WithoutCancel(ctx))
		r.mu.Unlock()
		if _, err := r.ReloadAll(ctx, 0); err != nil {
			return err
		}
		r.mu.Lock()
		r.ready = true
		r.mu.Unlock()
		input := r.Input()
		r.Go(func() {
			for e := range input {
				r.dispatch(e)
			}
		})
		return nil
	}, r.shutdownHooks())
}

func (r *Router) reserveCapacity(weight int64) bool {
	for {
		old := r.capacity.Load()
		if weight < 0 || weight > r.options.MaxRetainedSize-old {
			return false
		}
		if r.capacity.CompareAndSwap(old, old+weight) {
			return true
		}
	}
}

func (r *Router) reserve(weight int64) bool {
	if !r.reserveCapacity(weight) {
		return false
	}
	r.memory.Add(weight)
	return true
}
func (r *Router) releaseMemory(weight int64) { r.memory.Add(-weight); r.capacity.Add(-weight) }
func (r *Router) queueCapacity(size int) int64 {
	return min(int64(size+1)*(r.options.MaxEventSize*8+256), r.options.MaxQueueRetainedSize)
}

func reserveQueue(counter *atomic.Int64, weight, limit int64) bool {
	for {
		old := counter.Load()
		if weight > limit-old {
			return false
		}
		if counter.CompareAndSwap(old, old+weight) {
			return true
		}
	}
}

func (r *Router) startDiagnostics(ctx context.Context) error {
	r.diagnosticMu.Lock()
	defer r.diagnosticMu.Unlock()
	if r.diagnosticsDone != nil {
		select {
		case <-r.diagnosticsDone:
		default:
			return errors.New("managed diagnostic cleanup pending")
		}
	}
	// Reserve metadata storage too; diagnostic sinks cannot consume another
	// lane's memory allowance. Smaller process budgets get a smaller queue.
	entryWeight := int64(1024 + 80*r.options.MaxRules)
	queueSize := min(1024, max(1, int(r.options.MaxRetainedSize/(32*entryWeight))))
	weight := int64(queueSize+1) * entryWeight
	if !r.reserve(weight) {
		return errors.New("diagnostic queue budget exceeded")
	}
	diagnostics := make(chan diagnosticEntry, queueSize)
	done := make(chan struct{})
	logger := r.options.Logger
	r.diagnostics, r.diagnosticsDone = diagnostics, done
	go func() {
		defer close(done)
		defer r.releaseMemory(weight)
		for entry := range diagnostics {
			logger.LogAttrs(ctx, entry.level, entry.message, entry.attrs...)
		}
	}()
	return nil
}

// Every attribute is copied metadata; queued diagnostics never own events or configurations.
func (r *Router) diagnostic(level slog.Level, message string, attrs ...slog.Attr) {
	r.diagnosticMu.Lock()
	defer r.diagnosticMu.Unlock()
	select {
	case r.diagnostics <- diagnosticEntry{level, message, attrs}:
	default:
		r.counters.diagnosticDropped.Add(1)
	}
}

func (r *Router) stopDiagnostics() {
	r.diagnosticMu.Lock()
	if r.diagnostics != nil {
		close(r.diagnostics)
		r.diagnostics = nil
	}
	done := r.diagnosticsDone
	r.diagnosticMu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-r.runCtx.Done():
		// An arbitrary sink cannot be interrupted. Its one worker can finish the
		// closed, bounded metadata queue later; restart waits for that cleanup.
	}
}

func (r *Router) dispatch(e event.Event) {
	started := time.Now()
	encoded, err := json.Marshal(e)
	weight := int64(len(encoded))*8 + 256
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lanes) == 0 {
		return
	}
	r.sequence++
	sequence := r.sequence
	for _, lane := range r.lanes {
		customer := lane.id
		if err != nil || int64(len(encoded)) > r.options.MaxEventSize || !reserveQueue(&lane.retained, weight, r.queueCapacity(r.options.InputQueue)) {
			lane.counters.inputDropped.Add(1)
			r.counters.inputDropped.Add(1)
			revision := lane.active.prepared.config.Revision
			r.record("input_dropped", customer, "", sequence, revision, nil)
			continue
		}
		r.memory.Add(weight)
		config := lane.active
		config.refs.Add(1)
		select {
		case lane.input <- item{e, sequence, config, weight}:
		default:
			lane.retained.Add(-weight)
			config.release()
			r.memory.Add(-weight)
			lane.counters.inputDropped.Add(1)
			r.counters.inputDropped.Add(1)
			revision := config.prepared.config.Revision
			r.record("input_dropped", customer, "", sequence, revision, nil)
		}
	}
	elapsed := time.Since(started).Seconds()
	r.diagnostic(slog.LevelDebug, "managed dispatch",
		slog.String("component", "managed.router"), slog.String("action", "dispatched"),
		slog.Uint64("occurrence_id", sequence), slog.Float64("admission_seconds", elapsed))
}

func (r *Router) newLane(id string, active *appliedCustomer) *customerLane {
	lane := &customerLane{id: id, active: active, input: make(chan item, r.options.InputQueue), done: make(chan struct{}), router: r}
	r.workers.Go(func() {
		defer r.capacity.Add(-r.queueCapacity(r.options.InputQueue))
		defer close(lane.done)
		for i := range lane.input {
			lane.process(i)
		}
	})
	return lane
}

func (c *customerLane) process(i item) {
	defer i.config.release()
	defer c.retained.Add(-i.weight)
	defer c.router.memory.Add(-i.weight)
	r := c.router
	if r.matchGate != nil {
		r.matchGate(r.runCtx, c.id)
	}
	if r.runCtx.Err() != nil {
		c.counters.canceled.Add(1)
		r.counters.canceled.Add(1)
		r.record("input_canceled", c.id, "", i.occurrence, i.config.prepared.config.Revision, nil)
		return
	}
	routes := map[*destination][]string{}
	for _, rule := range i.config.prepared.rules {
		if !rule.matcher.Match(i.event) {
			continue
		}
		c.counters.matched.Add(1)
		r.counters.matched.Add(1)
		r.diagnostic(slog.LevelDebug, "managed filter match",
			slog.String("component", "managed.router"), slog.String("action", "filter_matched"),
			slog.String("customer_id", c.id), slog.String("rule_id", rule.ID), slog.String("event_type", rule.EventType),
			slog.Uint64("revision", i.config.prepared.config.Revision), slog.Uint64("occurrence_id", i.occurrence))
		for id, destination := range i.config.destinations {
			if rule.Destination == "all" || rule.Destination == id {
				routes[destination] = append(routes[destination], rule.ID)
			}
		}
	}
	for d, rules := range routes {
		sort.Strings(rules)
		s := submission{i.event, i.occurrence, i.config.prepared.config.Revision, rules, i.weight}
		if !reserveQueue(&d.retained, s.weight, r.queueCapacity(r.options.DestinationQueue)) {
			d.drop(s, false)
			continue
		}
		r.memory.Add(s.weight)
		d.mu.Lock()
		if d.revoked {
			d.mu.Unlock()
			r.memory.Add(-s.weight)
			d.retained.Add(-s.weight)
			d.drop(s, true)
			continue
		}
		select {
		case d.queue <- s:
			d.mu.Unlock()
		default:
			d.mu.Unlock()
			r.memory.Add(-s.weight)
			d.retained.Add(-s.weight)
			d.drop(s, false)
		}
	}
}

func (r *Router) record(action, customer, destination string, occurrence, revision uint64, rules []string) {
	r.diagnostic(slog.LevelInfo, "managed route",
		slog.String("component", "managed.router"), slog.String("action", action),
		slog.String("customer_id", customer), slog.String("destination_id", destination),
		slog.Uint64("occurrence_id", occurrence), slog.Uint64("revision", revision),
		slog.Any("rule_ids", append([]string(nil), rules...)))
}

func (r *Router) configured(customer string, revision uint64, rule Rule) {
	r.diagnostic(slog.LevelInfo, "managed filter configured",
		slog.String("component", "managed.router"), slog.String("action", "filter_configured"),
		slog.String("customer_id", customer), slog.String("rule_id", rule.ID),
		slog.String("event_type", rule.EventType), slog.Uint64("revision", revision))
}

// Stop drains router-owned work for a bounded period, then cancels pending work.
func (r *Router) Stop() error { return r.Shutdown(r.shutdownHooks()) }

func (r *Router) shutdownHooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{
		BeforeWait: func() error {
			r.stopping.Store(true)
			r.mu.Lock()
			r.ready = false
			cancel := r.cancel
			r.mu.Unlock()
			if cancel != nil {
				time.AfterFunc(r.options.ShutdownTimeout, cancel)
			}
			return nil
		},
		AfterWait: func() error {
			r.reload <- struct{}{}
			defer func() { <-r.reload }()
			r.mu.Lock()
			lanes := r.lanes
			r.lanes = map[string]*customerLane{}
			for _, lane := range lanes {
				close(lane.input)
			}
			r.mu.Unlock()
			for _, lane := range lanes {
				<-lane.done
			}
			unique := map[*destination]bool{}
			for _, lane := range lanes {
				for _, d := range lane.active.destinations {
					unique[d] = true
				}
			}
			for d := range unique {
				d.drain()
			}
			r.workers.Wait()
			r.stopDiagnostics()
			for _, lane := range lanes {
				lane.active.release()
			}
			r.releaseMemory(r.historyWeight)
			r.historyWeight = 0
			r.history = nil
			if r.cancel != nil {
				r.cancel()
			}
			return nil
		},
	}
}

// CustomerStatus exposes only opaque configuration identities and counters.
type CustomerStatus struct {
	Cutover      uint64                       `json:"cutover_occurrence_id"`
	Revision     uint64                       `json:"revision"`
	InputQueued  int                          `json:"input_queued"`
	Counters     Counters                     `json:"counters"`
	Destinations map[string]DestinationStatus `json:"destinations"`
}

// DestinationStatus distinguishes pending work from degraded plugin state.
type DestinationStatus struct {
	State      string   `json:"state"`
	Queued     int      `json:"queued"`
	LimitGroup string   `json:"limit_group,omitempty"`
	Counters   Counters `json:"counters"`
}

// Status reports the current applied configuration, never credential or payload data.
type Status struct {
	Ready              bool                      `json:"ready"`
	ObservedGeneration uint64                    `json:"observed_generation"`
	AppliedGeneration  uint64                    `json:"applied_generation"`
	LastDispatched     uint64                    `json:"last_dispatched"`
	ValidationError    string                    `json:"validation_error,omitempty"`
	Customers          map[string]CustomerStatus `json:"customers"`
	Counters           Counters                  `json:"counters"`
	RetainedSize       int64                     `json:"retained_size"`
	PluginInstances    int64                     `json:"plugin_instances"`
	Capabilities       []Capability              `json:"capabilities"`
}

// Status returns a consistent configuration view with concurrent queue counters.
func (r *Router) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := Status{Ready: r.ready, ObservedGeneration: r.observed, AppliedGeneration: r.applied, LastDispatched: r.sequence, ValidationError: r.validation, Customers: map[string]CustomerStatus{}, Counters: r.counters.value(), RetainedSize: r.memory.Load(), PluginInstances: r.instances.Load(), Capabilities: Capabilities()}
	for id, lane := range r.lanes {
		c := CustomerStatus{Cutover: lane.active.cutover, Revision: lane.active.prepared.config.Revision, InputQueued: len(lane.input), Counters: lane.counters.value(), Destinations: map[string]DestinationStatus{}}
		for id, d := range lane.active.destinations {
			d.mu.Lock()
			state := "ready"
			if d.degraded.Load() {
				state = "degraded"
			} else if d.pending.Load() > 0 || len(d.queue) > 0 {
				state = "pending"
			}
			c.Destinations[id] = DestinationStatus{state, len(d.queue), d.spec.config.LimitGroup, d.counters.value()}
			d.mu.Unlock()
		}
		status.Customers[id] = c
	}
	return status
}

var (
	_          plugin.ManagedPlugin = (*Router)(nil)
	errStopped                      = errors.New("managed router stopped")
)
