// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	logoutput "github.com/blinklabs-io/adder/output/log"
	"github.com/blinklabs-io/adder/output/webhook"
	"github.com/blinklabs-io/adder/plugin"
)

type destination struct {
	retained     atomic.Int64
	pluginErrors <-chan error
	mu           sync.Mutex
	router       *Router
	customer     string
	spec         preparedDestination
	plugin       plugin.ManagedPlugin
	queue        chan submission
	completed    chan struct{}
	done         chan struct{}
	cancel       context.CancelFunc
	ctx          context.Context
	revoked      bool
	closing      bool
	degraded     atomic.Bool
	pending      atomic.Int64
	counters     counterSet
	refs         atomic.Int64
	stopped      atomic.Bool
	released     sync.Once
	weight       int64
	transport    *limitedTransport
}

func (r *Router) prepareDestination(ctx context.Context, customer string, spec preparedDestination) (*destination, error) {
	if r.instances.Add(1) > int64(r.options.MaxInstances) {
		r.instances.Add(-1)
		return nil, errors.New("plugin instance budget exceeded")
	}
	weight := int64(r.options.DestinationQueue+plugin.DefaultEventBuffer+1) * 256
	queueBudget := r.queueCapacity(r.options.DestinationQueue)
	if !r.reserveCapacity(queueBudget) {
		r.instances.Add(-1)
		return nil, errors.New("destination queue budget exceeded")
	}
	if !r.reserve(weight) {
		r.capacity.Add(-queueBudget)
		r.instances.Add(-1)
		return nil, errors.New("retained memory budget exceeded")
	}
	d := &destination{router: r, customer: customer, spec: spec, queue: make(chan submission, r.options.DestinationQueue), completed: make(chan struct{}, 1), done: make(chan struct{}), weight: weight}
	d.ctx, d.cancel = context.WithCancel(r.runCtx)
	cleanup := func() { d.cancel(); r.instances.Add(-1); r.releaseMemory(weight); r.capacity.Add(-queueBudget) }
	p, err := plugin.GetPlugin(plugin.PluginTypeOutput, spec.config.Plugin, spec.values)
	if err != nil {
		cleanup()
		return nil, errors.New("plugin configuration rejected")
	}
	d.plugin = p
	observer := func() {
		select {
		case d.completed <- struct{}{}:
		default:
		}
	}
	switch p := p.(type) {
	case *logoutput.LogOutput:
		root, err := os.OpenRoot(r.options.LogDir)
		if err != nil {
			cleanup()
			return nil, errors.New("local output directory unavailable")
		}
		// Retain the directory descriptor only during setup; the plugin owns the file.
		defer root.Close()
		name := spec.values["path"].(string)
		p.SetFileOpener(func() (*os.File, error) {
			before, err := root.Lstat(name)
			flags := os.O_WRONLY | os.O_APPEND
			if errors.Is(err, os.ErrNotExist) {
				flags |= os.O_CREATE | os.O_EXCL
			} else if err != nil || !before.Mode().IsRegular() {
				return nil, errors.New("output must be a regular file")
			}
			f, err := openManagedFile(root, name, flags, 0o600)
			if err != nil {
				return nil, errors.New("output file unavailable")
			}
			after, err := f.Stat()
			if err != nil || !after.Mode().IsRegular() || (before != nil && !os.SameFile(before, after)) {
				_ = f.Close()
				return nil, errors.New("output file changed during open")
			}
			return f, nil
		})
		p.SetProcessingObserver(observer)
	case *webhook.WebhookOutput:
		r.groupMu.Lock()
		group := r.groups[spec.config.LimitGroup]
		interval := time.Duration(spec.config.IntervalMS) * time.Millisecond
		if group != nil && group.interval != interval {
			r.groupMu.Unlock()
			cleanup()
			return nil, errors.New("shared limit group interval mismatch")
		}
		if group == nil {
			group = &limitGroup{interval: interval}
			r.groups[spec.config.LimitGroup] = group
		}
		group.refs++
		r.groupMu.Unlock()
		d.transport = r.transport(group)
		p.SetHTTPClient(&http.Client{Transport: d.transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("managed redirects forbidden") }})
		p.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
		p.SetProcessingObserver(observer)
	default:
		cleanup()
		return nil, errors.New("registered plugin does not match supported adapter")
	}
	stopSetup := context.AfterFunc(ctx, d.cancel)
	// The instance outlives the reload request; AfterFunc above carries setup cancellation.
	err = p.StartContext(d.ctx) //nolint:contextcheck // Lifetime is owned by the router and destination.
	stopSetup()
	if err != nil || ctx.Err() != nil {
		d.cancel()
		_ = p.Stop()
		d.releaseGroup()
		cleanup()
		return nil, errors.New("plugin preparation failed")
	}
	d.pluginErrors = p.ErrorChan()
	r.workers.Go(d.run)
	return d, nil
}

func (d *destination) releaseGroup() {
	if d.transport == nil {
		return
	}
	d.transport.base.CloseIdleConnections()
	r := d.router
	r.groupMu.Lock()
	defer r.groupMu.Unlock()
	g := d.transport.group
	g.refs--
	if g.refs == 0 {
		delete(r.groups, d.spec.config.LimitGroup)
	}
}
func (d *destination) release() { d.refs.Add(-1); d.releaseResources() }
func (d *destination) releaseResources() {
	if d.refs.Load() == 0 && d.stopped.Load() {
		d.released.Do(func() {
			d.router.releaseMemory(d.weight)
			d.router.capacity.Add(-d.router.queueCapacity(d.router.options.DestinationQueue))
			d.router.instances.Add(-1)
		})
	}
}

func (d *destination) drop(s submission, canceled bool) {
	d.counters.destinationDropped.Add(1)
	d.router.counters.destinationDropped.Add(1)
	action := "destination_dropped"
	if canceled {
		d.counters.canceled.Add(1)
		d.router.counters.canceled.Add(1)
		action = "destination_canceled"
	}
	d.router.record(action, d.customer, d.spec.config.ID, s.occurrence, s.revision, s.rules)
}

func (d *destination) revoke() {
	d.mu.Lock()
	d.revoked = true
	if !d.closing {
		close(d.queue)
		d.closing = true
	}
	d.cancel()
	d.mu.Unlock()
}

func (d *destination) drain() {
	d.mu.Lock()
	if !d.closing {
		close(d.queue)
		d.closing = true
	}
	d.mu.Unlock()
}

func (d *destination) run() {
	defer close(d.done)
	defer func() {
		d.cancel()
		_ = d.plugin.Stop()
		for err := range d.pluginErrors {
			if err != nil {
				d.pluginError()
			}
		}
		d.releaseGroup()
		d.stopped.Store(true)
		d.releaseResources()
	}()
	errorsCh := d.plugin.ErrorChan()
	failed := d.plugin.Failed()
	for {
		select {
		case err, ok := <-errorsCh:
			if !ok {
				errorsCh = nil
				continue
			}
			if err != nil {
				d.pluginError()
			}
		case <-failed:
			failed = nil
			d.degraded.Store(true)
			d.revoke()
		case s, ok := <-d.queue:
			if !ok {
				return
			}
			d.process(s, &errorsCh)
		}
	}
}

func (d *destination) pluginError() {
	d.counters.pluginErrors.Add(1)
	d.router.counters.pluginErrors.Add(1)
	d.degraded.Store(true)
	d.router.record("plugin_error", d.customer, d.spec.config.ID, 0, 0, nil)
}

func (d *destination) process(s submission, errorsCh *<-chan error) {
	defer d.router.memory.Add(-s.weight)
	defer d.retained.Add(-s.weight)
	timer := time.NewTimer(d.router.options.SendTimeout)
	defer timer.Stop()
	d.mu.Lock()
	if d.revoked || d.ctx.Err() != nil {
		d.mu.Unlock()
		d.drop(s, true)
		return
	}
	// Serialize the actual nonblocking offer with generation revocation.
	select {
	case d.plugin.InputChan() <- s.event:
		d.pending.Store(1)
		d.mu.Unlock()
		d.counters.submitted.Add(1)
		d.router.counters.submitted.Add(1)
		d.counters.unknown.Add(1)
		d.router.counters.unknown.Add(1)
		d.router.record("submitted_to_plugin", d.customer, d.spec.config.ID, s.occurrence, s.revision, s.rules)
	default:
		d.mu.Unlock()
		d.drop(s, false)
		return
	}

	defer d.pending.Store(0)
	for {
		select {
		case <-d.completed:
			return
		case err, ok := <-*errorsCh:
			if !ok {
				*errorsCh = nil
			} else if err != nil {
				d.pluginError()
			}
		case <-d.plugin.Failed():
			d.degraded.Store(true)
			d.revoke()
			return
		case <-d.ctx.Done():
			d.router.record("submission_uncertain", d.customer, d.spec.config.ID, s.occurrence, s.revision, s.rules)
			return
		case <-timer.C:
			d.degraded.Store(true)
			d.revoke()
			d.router.record("submission_uncertain", d.customer, d.spec.config.ID, s.occurrence, s.revision, s.rules)
			return
		}
	}
}
