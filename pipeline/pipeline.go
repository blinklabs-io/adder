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
	"errors"
	"fmt"
	"sync"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
)

type Pipeline struct {
	inputs     []plugin.Plugin
	filters    []plugin.Plugin
	outputs    []plugin.Plugin
	filterChan chan event.Event
	outputChan chan event.Event
	errorChan  chan error
	doneChan   chan bool
	wg         sync.WaitGroup
	stopOnce   sync.Once
}

func New() *Pipeline {
	p := &Pipeline{
		filterChan: make(chan event.Event),
		outputChan: make(chan event.Event),
		errorChan:  make(chan error),
		doneChan:   make(chan bool),
	}
	return p
}

func (p *Pipeline) AddInput(input plugin.Plugin) {
	p.inputs = append(p.inputs, input)
}

func (p *Pipeline) AddFilter(filter plugin.Plugin) {
	p.filters = append(p.filters, filter)
}

func (p *Pipeline) AddOutput(output plugin.Plugin) {
	p.outputs = append(p.outputs, output)
}

// ErrorChan is read-only
func (p *Pipeline) ErrorChan() <-chan error {
	return p.errorChan
}

// Start initiates the configured plugins and starts the necessary background processes to run the pipeline
func (p *Pipeline) Start() error {
	// Check if doneChan is already closed this happens if pipeline was stopped
	// A stopped pipeline cannot be restarted
	select {
	case <-p.doneChan:
		return errors.New("cannot start a stopped pipeline")
	default:
		// continue
	}

	// Start inputs
	for _, input := range p.inputs {
		if err := input.Start(); err != nil {
			return fmt.Errorf("failed to start input: %w", err)
		}
		// Start background process to send input events to combined filter channel
		p.wg.Add(1)
		go p.chanCopyLoop(input.OutputChan(), p.filterChan)
		// Start background error listener
		p.wg.Add(1)
		go p.errorChanWait(input.ErrorChan())
	}
	// Start filters
	for idx, filter := range p.filters {
		if err := filter.Start(); err != nil {
			return fmt.Errorf("failed to start filter: %w", err)
		}
		if idx == 0 {
			// Start background process to send events from combined filter channel to first filter plugin
			p.wg.Add(1)
			go p.chanCopyLoop(p.filterChan, filter.InputChan())
		} else {
			// Start background process to send events from previous filter plugin to current filter plugin
			p.wg.Add(1)
			go p.chanCopyLoop(p.filters[idx-1].OutputChan(), filter.InputChan())
		}
		if idx == len(p.filters)-1 {
			// Start background process to send events from last filter to combined output channel
			p.wg.Add(1)
			go p.chanCopyLoop(filter.OutputChan(), p.outputChan)
		}
		// Start background error listener
		p.wg.Add(1)
		go p.errorChanWait(filter.ErrorChan())
	}
	if len(p.filters) == 0 {
		// Start background process to send events from combined filter channel to combined output channel if
		// there are no filter plugins
		p.wg.Add(1)
		go p.chanCopyLoop(p.filterChan, p.outputChan)
	}
	// Start outputs
	for _, output := range p.outputs {
		if err := output.Start(); err != nil {
			return fmt.Errorf("failed to start output: %w", err)
		}
		// Start background error listener
		p.wg.Add(1)
		go p.errorChanWait(output.ErrorChan())
	}
	p.wg.Add(1)
	go p.outputChanLoop()
	return nil
}

// Stop shuts down the pipeline and all plugins
// Stop is idempotent and safe to call multiple times
// A stopped pipeline cannot be restarted
func (p *Pipeline) Stop() error {
	var stopErrors []error

	p.stopOnce.Do(func() {
		close(p.doneChan)
		p.wg.Wait()

		// Stop plugins and collect errors
		for _, input := range p.inputs {
			if err := input.Stop(); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("failed to stop input: %w", err))
			}
		}
		for _, filter := range p.filters {
			if err := filter.Stop(); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("failed to stop filter: %w", err))
			}
		}
		for _, output := range p.outputs {
			if err := output.Stop(); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("failed to stop output: %w", err))
			}
		}

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

// outputChanLoop reads events from the output channel and writes them to each output plugin's input channel
func (p *Pipeline) outputChanLoop() {
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
			for _, output := range p.outputs {
				select {
				case output.InputChan() <- evt:
				case <-p.doneChan:
					return
				}
			}
		}
	}
}

// errorChanWait reads from an error channel. If an error is received, it's copied to the plugin error channel
func (p *Pipeline) errorChanWait(errorChan chan error) {
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
