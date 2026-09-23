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

package plugin_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartRunConcurrentStartsAndRestart(t *testing.T) {
	var b plugin.Base
	var setups, cleanups atomic.Int32
	setup := func(context.Context) error {
		setups.Add(1)
		b.Go(func() { <-b.Done() })
		return nil
	}
	hooks := plugin.ShutdownHooks{AfterWait: func() error {
		cleanups.Add(1)
		return nil
	}}
	cfg := plugin.BaseConfig{HasInput: true}
	results := make(chan error, 16)
	for range 16 {
		go func() { results <- b.StartRun(context.Background(), cfg, setup, hooks) }()
	}
	for range 16 {
		require.NoError(t, <-results)
	}
	assert.Equal(t, int32(1), setups.Load())
	assert.True(t, b.Running())
	in, ctx := b.Input(), b.Context()
	require.NoError(t, b.Shutdown(hooks))
	require.NoError(t, b.Shutdown(hooks))
	assert.Equal(t, int32(1), cleanups.Load())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NoError(t, b.StartRun(context.Background(), cfg, setup, hooks))
	assert.NotEqual(t, in, b.Input())
	assert.Equal(t, int32(2), setups.Load())
	require.NoError(t, b.Shutdown(hooks))
}

func TestStartRunFailureUnwindsWorkersAndResources(t *testing.T) {
	var b plugin.Base
	setupErr, cleanupErr := errors.New("setup"), errors.New("cleanup")
	var exited atomic.Bool
	var cleaned bool
	hooks := plugin.ShutdownHooks{AfterWait: func() error {
		cleaned = exited.Load()
		return cleanupErr
	}}
	err := b.StartRun(context.Background(),
		plugin.BaseConfig{HasInput: true},
		func(context.Context) error {
			assert.False(t, b.Running(), "setup is not a running plugin yet")
			b.Go(func() { <-b.Done(); exited.Store(true) })
			return setupErr
		},
		hooks,
	)
	require.ErrorIs(t, err, setupErr)
	require.ErrorIs(t, err, cleanupErr)
	assert.True(t, cleaned)
	assert.False(t, b.Running())
	assert.Nil(t, b.InputChan())
	assert.Nil(t, b.ErrorChan())
	require.NoError(t, b.Shutdown(hooks))
	require.NoError(
		t,
		b.StartRun(
			context.Background(),
			plugin.BaseConfig{},
			func(context.Context) error {
				return nil
			},
			plugin.ShutdownHooks{},
		),
	)
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
}

func TestShutdownCancelsSetupAndWaitsForCleanup(t *testing.T) {
	var b plugin.Base
	entered, cleanup, release := make(
		chan struct{},
	), make(
		chan struct{},
	), make(
		chan struct{},
	)
	hooks := plugin.ShutdownHooks{AfterWait: func() error {
		close(cleanup)
		<-release
		return nil
	}}
	started := make(chan error, 1)
	go func() {
		started <- b.StartRun(context.Background(), plugin.BaseConfig{}, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}, hooks)
	}()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- b.Shutdown(hooks) }()
	select {
	case <-cleanup:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Shutdown did not cancel setup")
	}
	select {
	case <-stopped:
		t.Error("Shutdown returned before cleanup")
	default:
	}
	close(release)
	require.ErrorIs(t, <-started, context.Canceled)
	require.NoError(t, <-stopped)
	assert.False(t, b.Running())
	assert.Nil(t, b.ErrorChan())
}

func TestErrorSendersRaceShutdown(t *testing.T) {
	for range 50 {
		var b plugin.Base
		plugintest.StartBase(t, &b, plugin.BaseConfig{})
		err := errors.New("callback")
		for range plugin.ErrorBuffer {
			require.True(t, b.TrySendError(err))
		}
		var senders sync.WaitGroup
		ready := make(chan struct{})
		for range 8 {
			senders.Go(func() {
				<-ready
				b.SendError(err)
				for range 20 {
					b.TrySendError(err)
				}
			})
		}
		close(ready)
		require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
		senders.Wait()
		assert.False(t, b.SendError(err))
		assert.False(t, b.TrySendError(err))
	}
}

func TestStartRunParentCancellation(t *testing.T) {
	var b plugin.Base
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	exited := make(chan struct{})
	var cleaned atomic.Bool
	started := make(chan error, 1)
	go func() {
		started <- b.StartRun(ctx, plugin.BaseConfig{}, func(run context.Context) error {
			b.Go(func() { <-run.Done(); close(exited) })
			close(entered)
			<-run.Done()
			return run.Err()
		}, plugin.ShutdownHooks{AfterWait: func() error {
			<-exited
			cleaned.Store(true)
			return nil
		}})
	}()
	<-entered
	cancel()
	select {
	case err := <-started:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not finish setup cleanup")
	}
	require.True(t, cleaned.Load())
	require.False(t, b.Running())
	require.Nil(t, b.ErrorChan())
}

func TestStartRunRejectsCanceledParentBeforeSetup(t *testing.T) {
	var b plugin.Base
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := b.StartRun(ctx, plugin.BaseConfig{}, func(context.Context) error {
		t.Fatal("setup called with canceled parent")
		return nil
	}, plugin.ShutdownHooks{})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, b.Running())
	require.Nil(t, b.ErrorChan())
	require.NoError(t, b.Shutdown(plugin.ShutdownHooks{}))
}
