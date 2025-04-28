package chainsync

import (
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/input/chainsync"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/assert"
)

func TestPluginRegistration(t *testing.T) {
	// Retrieve the plugin entries
	plugins := plugin.GetPlugins(
		plugin.PluginTypeFilter,
	) // Get all registered plugins

	// Find the "chainsync" plugin
	var p plugin.Plugin
	for _, entry := range plugins {
		if entry.Name == "chainsync" {
			// Create a new instance of the plugin
			p = entry.NewFromOptionsFunc()
			break
		}
	}

	// Verify that the plugin was found
	assert.NotNil(t, p, "Plugin should be registered")

	// Verify that the plugin implements the Plugin interface
	_, ok := p.(plugin.Plugin)
	assert.True(t, ok, "Plugin should implement the Plugin interface")
}

func TestPluginStartStop(t *testing.T) {
	// Create a new plugin instance
	p := NewFromCmdlineOptions()

	// Start the plugin
	err := p.Start()
	assert.NoError(t, err, "Plugin should start without errors")

	// Stop the plugin
	err = p.Stop()
	assert.NoError(t, err, "Plugin should stop without errors")
}

func TestPluginChannels(t *testing.T) {
	// Create a new plugin instance
	p := NewFromCmdlineOptions()

	// Verify that the error channel is not nil
	assert.NotNil(t, p.ErrorChan(), "Error channel should not be nil")

	// Verify that the input channel is not nil
	assert.NotNil(t, p.InputChan(), "Input channel should not be nil")

	// Verify that the output channel is not nil
	assert.NotNil(t, p.OutputChan(), "Output channel should not be nil")
}

func TestPluginEventProcessing(t *testing.T) {
	// Create a new plugin instance
	p := NewFromCmdlineOptions()

	// Start the plugin
	err := p.Start()
	assert.NoError(t, err, "Plugin should start without errors")

	// Create a test event with a TransactionEvent payload
	testEvent := event.Event{
		Type:      "transaction",
		Timestamp: time.Now(),
		Payload:   chainsync.TransactionEvent{},
	}

	// Send the event to the input channel
	p.InputChan() <- testEvent

	// Read the event from the output channel
	select {
	case outputEvent := <-p.OutputChan():
		assert.Equal(
			t,
			testEvent,
			outputEvent,
			"Output event should match input event",
		)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for output event")
	}

	// Stop the plugin
	err = p.Stop()
	assert.NoError(t, err, "Plugin should stop without errors")
}
