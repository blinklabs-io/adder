package pipeline

import (
	"fmt"

	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/plugin"
)

type Pipeline struct {
	inputs     []plugin.Plugin
	outputs    []plugin.Plugin
	outputChan chan event.Event
	errorChan  chan error
	doneChan   chan bool
}

func New() *Pipeline {
	p := &Pipeline{
		outputChan: make(chan event.Event),
		errorChan:  make(chan error),
		doneChan:   make(chan bool),
	}
	return p
}

func (p *Pipeline) AddInput(input plugin.Plugin) {
	p.inputs = append(p.inputs, input)
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
			return fmt.Errorf("failed to start input: %s", err)
		}
		// Start background process to send input events to combined output channel
		go p.chanCopyLoop(input.OutputChan(), p.outputChan)
		// Start background error listener
		go p.errorChanWait(input.ErrorChan())
	}
	// Start outputs
	for _, output := range p.outputs {
		if err := output.Start(); err != nil {
			return fmt.Errorf("failed to start output: %s", err)
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
	close(p.errorChan)
	close(p.outputChan)
	// Stop inputs
	for _, input := range p.inputs {
		if err := input.Stop(); err != nil {
			return fmt.Errorf("failed to stop input: %s", err)
		}
	}
	// Stop outputs
	for _, output := range p.outputs {
		if err := output.Stop(); err != nil {
			return fmt.Errorf("failed to stop output: %s", err)
		}
	}
	return nil
}

// chanCopyLoop is a generic function for reading an event from one channel and writing it to another in a loop
func (p *Pipeline) chanCopyLoop(input <-chan event.Event, output chan<- event.Event) {
	for {
		select {
		case <-p.doneChan:
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
	for {
		select {
		case <-p.doneChan:
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
