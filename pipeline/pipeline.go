// Copyright 2023 Blink Labs Software
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

func (p *Pipeline) ErrorChan() chan error {
	return p.errorChan
}

// Start initiates the configured plugins and starts the necessary background processes to run the pipeline
func (p *Pipeline) Start() error {
	// Start inputs
	for _, input := range p.inputs {
		if err := input.Start(); err != nil {
			return fmt.Errorf("failed to start input: %w", err)
		}
		// Start background process to send input events to combined filter channel
		go p.chanCopyLoop(input.OutputChan(), p.filterChan)
		// Start background error listener
		go p.errorChanWait(input.ErrorChan())
	}
	// Start filters
	for idx, filter := range p.filters {
		if err := filter.Start(); err != nil {
			return fmt.Errorf("failed to start input: %w", err)
		}
		if idx == 0 {
			// Start background process to send events from combined filter channel to first filter plugin
			go p.chanCopyLoop(p.filterChan, filter.InputChan())
		} else {
			// Start background process to send events from previous filter plugin to current filter plugin
			go p.chanCopyLoop(p.filters[idx-1].OutputChan(), filter.InputChan())
		}
		if idx == len(p.filters)-1 {
			// Start background process to send events from last filter to combined output channel
			go p.chanCopyLoop(filter.OutputChan(), p.outputChan)
		}
		// Start background error listener
		go p.errorChanWait(filter.ErrorChan())
	}
	if len(p.filters) == 0 {
		// Start background process to send events from combined filter channel to combined output channel if
		// there are no filter plugins
		go p.chanCopyLoop(p.filterChan, p.outputChan)
	}
	// Start outputs
	for _, output := range p.outputs {
		if err := output.Start(); err != nil {
			return fmt.Errorf("failed to start output: %w", err)
		}
		// Start background error listener
		go p.errorChanWait(output.ErrorChan())
	}
	go p.outputChanLoop()
	return nil
}

// Stop shuts down the pipeline and all plugins
func (p *Pipeline) Stop() error {
	close(p.doneChan)
	p.wg.Wait()
	close(p.errorChan)
	close(p.filterChan)
	close(p.outputChan)
	// Stop inputs
	for _, input := range p.inputs {
		if err := input.Stop(); err != nil {
			return fmt.Errorf("failed to stop input: %w", err)
		}
	}
	// Stop outputs
	for _, output := range p.outputs {
		if err := output.Stop(); err != nil {
			return fmt.Errorf("failed to stop output: %w", err)
		}
	}
	return nil
}

// chanCopyLoop is a generic function for reading an event from one channel and writing it to another in a loop
func (p *Pipeline) chanCopyLoop(
	input <-chan event.Event,
	output chan<- event.Event,
) {
	p.wg.Add(1)
	for {
		select {
		case <-p.doneChan:
			p.wg.Done()
			return
		case evt, ok := <-input:
			if ok {
				// Copy input event to output chan
				output <- evt
			}
		}
	}
}

// outputChanLoop reads events from the output channel and writes them to each output plugin's input channel
func (p *Pipeline) outputChanLoop() {
	p.wg.Add(1)
	for {
		select {
		case <-p.doneChan:
			p.wg.Done()
			return
		case evt, ok := <-p.outputChan:
			if ok {
				// Send event to all output plugins
				for _, output := range p.outputs {
					output.InputChan() <- evt
				}
			}
		}
	}
}

// errorChanWait reads from an error channel. If an error is received, it's copied to the plugin error channel and the plugin stopped
func (p *Pipeline) errorChanWait(errorChan chan error) {
	err, ok := <-errorChan
	if ok {
		p.errorChan <- err
		_ = p.Stop()
	}
}
