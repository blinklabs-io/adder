// Copyright 2025 Blink Labs Software
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

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

// Pipeline owns the lifecycle and connections of its configured plugins.
// Configure it before Start. Do not modify its topology during a run, share
// plugin instances between pipelines, or restart connected plugins individually.
type Pipeline struct {
	filterChan   chan event.Event
	outputChan   chan event.Event
	errorChan    chan error
	doneChan     chan bool
	inputs       []plugin.ManagedPlugin
	filters      []plugin.ManagedPlugin
	outputs      []plugin.ManagedPlugin
	observer     chan<- event.Event // optional observer for API /events
	observerMu   sync.RWMutex
	wg           sync.WaitGroup
	doneOnce     sync.Once
	stopOnce     sync.Once
	lifecycleMu  sync.Mutex
	running      bool
	runningMu    sync.RWMutex
	cancel       context.CancelFunc // guarded by runningMu
	started      []startedPlugin    // guarded by lifecycleMu
	stopRequests int                // guarded by runningMu
	failure      error              // guarded by runningMu
	failedChan   chan struct{}      // guarded by runningMu
}

func New() *Pipeline {
	p := &Pipeline{
		filterChan: make(chan event.Event),
		outputChan: make(chan event.Event),
		errorChan:  make(chan error),
		doneChan:   make(chan bool),
		failedChan: make(chan struct{}),
	}
	return p
}

// AddInput appends a source. Configure the topology before Start.
func (p *Pipeline) AddInput(input plugin.ManagedPlugin) {
	p.inputs = append(p.inputs, input)
}

// AddFilter appends a transform in event-processing order. Call before Start.
func (p *Pipeline) AddFilter(filter plugin.ManagedPlugin) {
	p.filters = append(p.filters, filter)
}

// AddOutput appends a sink in blocking broadcast order. Call before Start.
func (p *Pipeline) AddOutput(output plugin.ManagedPlugin) {
	p.outputs = append(p.outputs, output)
}

// RegisterObserver sets an observer channel that receives a copy of
// every event passing through the pipeline. Sends are non-blocking:
// if the channel is full, events are dropped. Only one observer is
// supported. Call before Start().
func (p *Pipeline) RegisterObserver(ch chan<- event.Event) {
	p.observerMu.Lock()
	defer p.observerMu.Unlock()
	p.observer = ch
}

// ErrorChan returns the pipeline's error channel (read-only).
// Note: After calling Stop() and Start() to restart the pipeline,
// consumers must call ErrorChan() again to get the new channel.
// References obtained before restart will point to a closed channel.
func (p *Pipeline) ErrorChan() <-chan error {
	p.runningMu.RLock()
	defer p.runningMu.RUnlock()
	return p.errorChan
}

// Start starts the pipeline with a background context. See StartContext.
func (p *Pipeline) Start() error {
	return p.StartContext(context.Background())
}

// StartContext initiates the configured plugins with a context governing setup
// and plugin work. Cancellation during setup rolls back acquired plugins.
// After successful startup, the caller must call Stop even if ctx is canceled.
// A stopped pipeline can be restarted by calling Start() again.
// Note: After restart, consumers must re-obtain channels via ErrorChan() as the old channels are closed.
// Stop interrupts setup through the context supplied to every plugin.
// A failed plugin must clean up its partial startup before returning.
// Outputs start first, followed by filters in reverse order, then inputs.
// Declared roles are validated before startup and ports before wiring.
// Outputs and observers are optional. Without them, events are drained and
// discarded so production can continue without consumers.
func (p *Pipeline) StartContext(parent context.Context) error {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if err := parent.Err(); err != nil {
		return err
	}
	if p.IsRunning() {
		return errors.New("pipeline is already running")
	}
	if len(p.started) > 0 {
		return errors.New("pipeline requires Stop after failure")
	}
	if err := p.validateTopology(); err != nil {
		return err
	}
	p.runningMu.Lock()
	if p.stopRequests > 0 {
		p.runningMu.Unlock()
		return context.Canceled
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.failure = nil
	// Check if doneChan is already closed (pipeline was stopped)
	// If so, recreate channels to allow restart
	select {
	case <-p.doneChan:
		p.doneChan = make(chan bool)
		p.filterChan = make(chan event.Event)
		p.outputChan = make(chan event.Event)
		p.errorChan = make(chan error)
		p.stopOnce = sync.Once{}
		p.doneOnce = sync.Once{}
		p.failedChan = make(chan struct{})
	default:
		// continue
	}
	p.runningMu.Unlock()

	rollback := func(startErr error) error {
		cancel()
		var rollbackErrs []error
		p.stopOnce.Do(func() {
			p.doneOnce.Do(func() { close(p.doneChan) })
			// Retire raw channel producers before plugins close their inputs.
			p.wg.Wait()
			rollbackErrs = p.stopPlugins()
			close(p.errorChan)
			close(p.filterChan)
			close(p.outputChan)
		})
		return errors.Join(startErr, p.Failure(), errors.Join(rollbackErrs...))
	}
	startPlugin := func(component plugin.ManagedPlugin, role plugin.PluginType, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := component.StartContext(ctx); err != nil {
			return fmt.Errorf(
				"failed to start %s[%d]: %w",
				plugin.PluginTypeName(role),
				index,
				err,
			)
		}
		p.started = append(p.started, startedPlugin{component, role, index})
		if err := validatePorts(component, role); err != nil {
			return fmt.Errorf(
				"invalid %s[%d] ports: %w",
				plugin.PluginTypeName(role),
				index,
				err,
			)
		}
		failed := component.Failed()
		if failed == nil {
			return fmt.Errorf(
				"%s[%d]: nil failure signal",
				plugin.PluginTypeName(role),
				index,
			)
		}
		p.wg.Add(1)
		go p.failureWait(component, failed, role, index)
		if errs := component.ErrorChan(); errs != nil {
			p.wg.Add(1)
			go p.errorChanWait(errs)
		}
		return nil
	}

	// Configured consumers and downstream links are ready before producers.
	// With no consumers, the terminal loop still drains and discards events.
	// Captured channels belong to this run only.
	destinations := make([]chan<- event.Event, 0, len(p.outputs))
	for idx, output := range p.outputs {
		if err := startPlugin(output, plugin.PluginTypeOutput, idx); err != nil {
			return rollback(err)
		}
		destinations = append(destinations, output.InputChan())
	}
	p.wg.Add(1)
	go p.outputChanLoop(destinations)

	downstream := (chan<- event.Event)(p.outputChan)
	for idx := len(p.filters) - 1; idx >= 0; idx-- {
		filter := p.filters[idx]
		if err := startPlugin(filter, plugin.PluginTypeFilter, idx); err != nil {
			return rollback(err)
		}
		p.wg.Add(1)
		go p.chanCopyLoop(filter.OutputChan(), downstream)
		downstream = filter.InputChan()
	}
	p.wg.Add(1)
	go p.chanCopyLoop(p.filterChan, downstream)

	for idx, input := range p.inputs {
		if err := startPlugin(input, plugin.PluginTypeInput, idx); err != nil {
			return rollback(err)
		}
		p.wg.Add(1)
		go p.chanCopyLoop(input.OutputChan(), p.filterChan)
	}

	p.runningMu.Lock()
	if err := ctx.Err(); err != nil {
		p.runningMu.Unlock()
		return rollback(err)
	}
	p.running = true
	p.runningMu.Unlock()

	return nil
}

// Stop shuts down the pipeline and the plugins it successfully started.
// Stop is idempotent and safe to call multiple times
// A stopped pipeline can be restarted by calling Start() again
// Stop cancels startup before waiting for the lifecycle lock. Setup must
// return before Stop can finish. Forwarding exits before
// plugins stop, so shutdown does not guarantee delivery of all buffered events.
func (p *Pipeline) Stop() error {
	// Cancellation must reach setup without waiting for the startup lock.
	p.runningMu.Lock()
	p.stopRequests++
	if p.cancel != nil {
		p.cancel()
	}
	p.runningMu.Unlock()
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	defer func() {
		p.runningMu.Lock()
		p.stopRequests--
		p.runningMu.Unlock()
	}()
	var stopErrors []error

	p.stopOnce.Do(func() {
		p.runningMu.Lock()
		p.running = false
		p.runningMu.Unlock()

		p.doneOnce.Do(func() { close(p.doneChan) })
		p.wg.Wait()

		stopErrors = p.stopPlugins()

		close(p.errorChan)
		close(p.filterChan)
		close(p.outputChan)
	})

	return errors.Join(stopErrors...)
}

// chanCopyLoop is a generic function for reading an event from one channel and writing it to another in a loop
func (p *Pipeline) chanCopyLoop(
	input <-chan event.Event,
	output chan<- event.Event,
) {
	defer p.wg.Done()
	for {
		select {
		case <-p.doneChan:
			return
		case evt, ok := <-input:
			if !ok {
				return
			}
			select {
			// Pass input event to output chan
			case output <- evt:
			case <-p.doneChan:
				return
			}
		}
	}
}

// outputChanLoop drains events even without destinations or an observer.
func (p *Pipeline) outputChanLoop(destinations []chan<- event.Event) {
	defer p.wg.Done()
	for {
		select {
		case <-p.doneChan:
			return
		case evt, ok := <-p.outputChan:
			if !ok {
				return
			}
			// Send event to all output plugins
			for _, destination := range destinations {
				select {
				case destination <- evt:
				case <-p.doneChan:
					return
				}
			}
			// Non-blocking send to observer (if registered)
			p.observerMu.RLock()
			obs := p.observer
			p.observerMu.RUnlock()
			if obs != nil {
				select {
				case obs <- evt:
				default:
					// observer full, drop event
				}
			}
		}
	}
}

// errorChanWait reads from a plugin error channel and forwards errors to the pipeline error channel
func (p *Pipeline) errorChanWait(errorChan <-chan error) {
	defer p.wg.Done()
	for {
		select {
		case <-p.doneChan:
			return
		case err, ok := <-errorChan:
			if !ok {
				// Channel closed
				return
			}
			// Forward plugin error to pipeline error channel
			select {
			case p.errorChan <- err:
			case <-p.doneChan:
				return
			}
		}
	}
}

// IsRunning returns true if the pipeline is currently running
func (p *Pipeline) IsRunning() bool {
	p.runningMu.RLock()
	defer p.runningMu.RUnlock()
	return p.running
}

// Failed closes when a plugin reports terminal failure. Forwarding is canceled
// and IsRunning becomes false. The owner must call Stop to release resources.
// Normal Stop does not close it; reacquire it after restarting the pipeline.
func (p *Pipeline) Failed() <-chan struct{} {
	p.runningMu.RLock()
	defer p.runningMu.RUnlock()
	return p.failedChan
}

// Failure returns the first terminal plugin error until the next run starts.
func (p *Pipeline) Failure() error {
	p.runningMu.RLock()
	defer p.runningMu.RUnlock()
	return p.failure
}

func (p *Pipeline) failureWait(
	reporter plugin.FailureReporter,
	failed <-chan struct{},
	role plugin.PluginType,
	index int,
) {
	defer p.wg.Done()
	select {
	case <-p.doneChan:
		return
	case <-failed:
	}
	err := reporter.Failure()
	if err == nil {
		err = errors.New("plugin reported failure without an error")
	}
	p.runningMu.Lock()
	defer p.runningMu.Unlock()
	if p.failure != nil || p.stopRequests > 0 {
		return
	}
	p.failure = fmt.Errorf(
		"%s[%d]: %w",
		plugin.PluginTypeName(role),
		index,
		err,
	)
	p.running = false
	p.cancel()
	p.doneOnce.Do(func() { close(p.doneChan) })
	close(p.failedChan)
}
