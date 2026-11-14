// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReloadResult describes a configuration switch, not delivery or queue drainage.
type ReloadResult struct {
	Generation uint64            `json:"generation"`
	Revisions  map[string]uint64 `json:"revisions"`
	Cutover    uint64            `json:"cutover_occurrence_id"`
}

// ReloadAll applies an entire manifest generation at one dispatch boundary.
// Generation zero is reserved for initial startup.
func (r *Router) ReloadAll(ctx context.Context, generation uint64) (ReloadResult, error) {
	return r.reloadConfig(ctx, "", generation)
}

// ReloadCustomer changes only an existing customer and requires an unchanged roster.
func (r *Router) ReloadCustomer(ctx context.Context, id string, revision uint64) (ReloadResult, error) {
	if !identifier.MatchString(id) || revision == 0 {
		return ReloadResult{}, errors.New("invalid customer reload request")
	}
	return r.reloadConfig(ctx, id, revision)
}

func (r *Router) reloadConfig(parent context.Context, id string, expected uint64) (result ReloadResult, err error) {
	ctx, cancel := context.WithTimeout(parent, r.options.ReloadTimeout)
	defer cancel()
	select {
	case r.reload <- struct{}{}:
		defer func() { <-r.reload }()
	case <-ctx.Done():
		return result, errors.New("reload deadline exceeded")
	}
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err != nil {
			r.validation = err.Error()
		} else {
			r.validation = ""
		}
	}()
	r.mu.Lock()
	stopped := r.stopping.Load() || r.runCtx == nil || r.runCtx.Err() != nil
	r.mu.Unlock()
	if stopped {
		return result, errStopped
	}
	manifest, digest, err := r.loadManifest()
	r.mu.Lock()
	r.observed = manifest.Generation
	r.mu.Unlock()
	if err != nil {
		return result, err
	}
	if manifest.Generation < r.applied {
		return result, errors.New("stale manifest generation")
	}
	if manifest.Generation == r.applied && digest != r.manifestDigest {
		return result, errors.New("manifest content changed without advancing generation")
	}
	if id == "" && manifest.Generation != expected && (expected != 0 || r.applied != 0) {
		return result, errors.New("expected manifest generation not observed")
	}
	var target *customerLane
	if id != "" {
		target = r.lanes[id]
		if target == nil {
			return result, errors.New("customer does not exist; use reload-all")
		}
		if len(manifest.Customers) != len(r.lanes) {
			return result, errors.New("customer roster changed; use reload-all")
		}
		for _, ref := range manifest.Customers {
			if r.lanes[ref.ID] == nil {
				return result, errors.New("customer roster changed; use reload-all")
			}
		}
	}
	prepared := map[string]*preparedCustomer{}
	for _, ref := range manifest.Customers {
		if id != "" && ref.ID != id {
			continue
		}
		if id != "" && ref.Revision != expected {
			return result, errors.New("expected customer revision not observed")
		}
		if ctx.Err() != nil {
			return result, errors.New("reload deadline exceeded")
		}
		p, err := r.loadCustomer(ref)
		if err != nil {
			return result, fmt.Errorf("customer %s: %w", ref.ID, err)
		}

		prepared[ref.ID] = p
	}
	nextHistory, historyWeight, err := advanceHistory(r.history, prepared, r.options)
	if err != nil {
		return result, err
	}
	historyDelta := historyWeight - r.historyWeight
	if !r.reserve(historyDelta) {
		return result, errors.New("revision history budget exceeded")
	}
	defer func() {
		if err != nil {
			r.releaseMemory(historyDelta)
		}
	}()
	staged := map[string]*appliedCustomer{}
	newLanes := map[string]*customerLane{}
	defer func() {
		if err == nil {
			return
		}
		retiring := map[*destination]struct{}{}
		for _, lane := range newLanes {
			close(lane.input)
		}
		for _, a := range staged {
			for _, d := range a.destinations {
				if !r.destinationActive(d) {
					d.revoke()
					retiring[d] = struct{}{}
				}
			}
			a.release()
		}
		// Staged adapters have received no events. Join their canceled workers
		// before a retry can compete for resources they are releasing.
		cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), min(r.options.ReloadTimeout, 5*time.Second))
		defer cancelCleanup()
		for d := range retiring {
			select {
			case <-d.done:
			case <-cleanup.Done():
				err = errors.Join(err, errors.New("staged cleanup still pending"))
				return
			}
		}
		for _, lane := range newLanes {
			select {
			case <-lane.done:
			case <-cleanup.Done():
				err = errors.Join(err, errors.New("staged cleanup still pending"))
				return
			}
		}
	}()
	for id, p := range prepared {
		old := r.lanes[id]
		if old != nil && old.active.prepared.digest == p.digest {
			continue
		}
		weight := p.size*8 + int64(len(p.rules))*256
		if !r.reserve(weight) {
			return result, errors.New("retained configuration budget exceeded")
		}
		a := &appliedCustomer{prepared: p, destinations: map[string]*destination{}, router: r, weight: weight}
		a.refs.Store(1)
		staged[id] = a
		if old == nil {
			if !r.reserveCapacity(r.queueCapacity(r.options.InputQueue)) {
				return result, errors.New("customer queue budget exceeded")
			}
			newLanes[id] = r.newLane(id, a)
		}
		for destID, spec := range p.specs {
			var d *destination
			if old != nil {
				existing := old.active.destinations[destID]
				if existing != nil && existing.spec.digest == spec.digest {
					d = existing
				}
			}
			if d == nil {
				d, err = r.prepareDestination(ctx, id, spec)
				if err != nil {
					return result, err
				}
			}
			d.refs.Add(1)
			a.destinations[destID] = d
		}
	}
	if ctx.Err() != nil {
		return result, errors.New("reload deadline exceeded")
	}
	_, currentDigest, readErr := r.loadManifest()
	if readErr != nil || currentDigest != digest {
		return result, errors.New("manifest changed during reload")
	}

	if r.beforeSwitch != nil {
		r.beforeSwitch()
	}
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return result, errors.New("reload deadline exceeded")
	}
	if r.stopping.Load() || r.runCtx.Err() != nil {
		r.mu.Unlock()
		return result, errStopped
	}
	for customer, lane := range r.lanes {
		if id != "" && id != customer {
			continue
		}
		if prepared[customer] == nil {
			for _, d := range lane.active.destinations {
				d.revoke()
			}
			close(lane.input)
			lane.active.release()
			delete(r.lanes, customer)
			continue
		}
		if next := staged[customer]; next != nil {
			for key, d := range lane.active.destinations {
				if next.destinations[key] != d {
					d.revoke()
				}
			}
			lane.active.release()
			lane.active = next
		}
	}
	for _, next := range staged {
		next.cutover = r.sequence
	}
	for customer, lane := range newLanes {
		r.lanes[customer] = lane
	}
	if id == "" {
		if manifest.Generation != r.applied {
			r.allCutover = r.sequence
		}
		r.applied = manifest.Generation
		r.manifestDigest = digest
	}
	r.history = nextHistory
	r.historyWeight = historyWeight
	cutover := r.allCutover
	if target != nil {
		cutover = target.active.cutover
	}
	result = ReloadResult{Generation: manifest.Generation, Revisions: map[string]uint64{}, Cutover: cutover}
	for customer, configuration := range prepared {
		result.Revisions[customer] = configuration.config.Revision
	}
	for customer, next := range staged {
		for _, rule := range next.prepared.rules {
			r.configured(customer, next.prepared.config.Revision, rule.Rule)
		}
	}
	r.mu.Unlock()
	return result, nil
}

func (r *Router) destinationActive(d *destination) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lane := range r.lanes {
		for _, current := range lane.active.destinations {
			if current == d {
				return true
			}
		}
	}
	return false
}
