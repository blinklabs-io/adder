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

// Package plugintest provides reusable lifecycle checks for plugin authors.
// Fixtures must start without external services. The helpers require Base's
// diagnostic methods in addition to the plugin interfaces.
package plugintest

import (
	"context"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StartBase starts a channel-only fixture through the public lifecycle.
// Tests requiring resources or workers should supply their own setup and hooks.
func StartBase(t *testing.T, b *plugin.Base, cfg plugin.BaseConfig) {
	t.Helper()
	require.NoError(t, b.StartRun(context.Background(), cfg,
		func(context.Context) error { return nil }, plugin.ShutdownHooks{}))
}

// FailedStart checks that a setup error leaves no live run or channels.
func FailedStart(t *testing.T, p interface {
	plugin.ManagedPlugin
	Running() bool
	Context() context.Context
},
) error {
	t.Helper()
	err := p.StartContext(context.Background())
	require.Error(t, err)
	assert.False(t, p.Running())
	assert.Nil(t, p.InputChan())
	assert.Nil(t, p.OutputChan())
	assert.Nil(t, p.ErrorChan())
	require.ErrorIs(t, p.Context().Err(), context.Canceled)
	require.NoError(t, p.Stop())
	return err
}

// Lifecycle checks duplicate and concurrent calls, channel ownership, and
// restart on an instance configured to start without external services.
func Lifecycle(t *testing.T, p interface {
	plugin.ManagedPlugin
	Running() bool
	Done() <-chan struct{}
	Context() context.Context
},
) {
	t.Helper()
	role := p.Role()
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	var previous <-chan error
	var previousFailure <-chan struct{}
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		results := make(chan error, 8)
		for range 8 {
			go func() { results <- p.StartContext(ctx) }()
		}
		for range 8 {
			select {
			case err := <-results:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("concurrent Start did not finish")
			}
		}
		failed := p.Failed()
		require.NotNil(t, failed)
		assert.NotEqual(t, previousFailure, failed)
		require.NoError(t, p.Failure())
		assert.True(t, p.Running())
		in, out, errs, done := p.InputChan(), p.OutputChan(), p.ErrorChan(), p.Done()
		switch role {
		case plugin.PluginTypeInput:
			require.Nil(t, in)
			require.NotNil(t, out)
		case plugin.PluginTypeFilter:
			require.NotNil(t, in)
			require.NotNil(t, out)
		case plugin.PluginTypeOutput:
			require.NotNil(t, in)
			require.Nil(t, out)
		default:
			t.Fatalf("invalid plugin role: %d", role)
		}
		require.NotNil(t, errs)
		assert.NotEqual(t, previous, errs)
		require.NoError(t, p.StartContext(context.Background()))
		assert.Equal(t, in, p.InputChan())
		assert.Equal(t, out, p.OutputChan())
		assert.Equal(t, errs, p.ErrorChan())
		assert.Equal(t, done, p.Done())
		cancel()
		require.ErrorIs(t, p.Context().Err(), context.Canceled)
		for range 8 {
			go func() { results <- p.Stop() }()
		}
		for range 8 {
			select {
			case err := <-results:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("concurrent Stop did not finish")
			}
		}
		assert.False(t, p.Running())
		assert.Nil(t, p.InputChan())
		assert.Nil(t, p.OutputChan())
		assert.Nil(t, p.ErrorChan())
		select {
		case <-done:
		default:
			t.Fatal("Stop did not close Done")
		}
		select {
		case <-failed:
			t.Fatal("normal cancellation or Stop reported a terminal failure")
		default:
		}
		previousFailure = failed
		previous = errs
	}
}
