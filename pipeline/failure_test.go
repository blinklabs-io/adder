// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pipeline_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestTerminalFailureCancelsBlockedChainAndAllowsRestartAfterStop(
	t *testing.T,
) {
	source := newChainPlugin(plugin.PluginTypeInput, "source", nil)
	sink := newChainPlugin(plugin.PluginTypeOutput, "sink", nil)
	exited := make(chan struct{}, 2)
	source.setup = func(context.Context) error {
		source.Go(func() {
			defer func() { exited <- struct{}{} }()
			for range 10000 {
				if !source.Emit(event.Event{Type: "test"}) {
					return
				}
			}
		})
		return nil
	}
	p := pipeline.New()
	p.AddInput(source)
	p.AddOutput(sink)
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	var previous <-chan struct{}
	for range 2 {
		require.NoError(t, p.Start())
		failed := p.Failed()
		require.NotEqual(t, previous, failed)
		require.NoError(t, p.Failure())
		// No error-channel reader and no sink worker: both diagnostic and event
		// paths can be blocked, but terminal notification must still get through.
		for range plugin.ErrorBuffer + 1 {
			sink.TrySendError(errors.New("recoverable"))
		}
		want := errors.New("sink worker died")
		sink.Fail(want)
		select {
		case <-failed:
		case <-time.After(time.Second):
			t.Fatal("failure was blocked")
		}
		require.False(t, p.IsRunning())
		require.ErrorIs(t, p.Failure(), want)
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Fatal("producer remained blocked")
		}
		require.Error(t, p.Start(), "failed run must be stopped before restart")
		require.NoError(t, p.Stop())
		require.ErrorIs(t, p.Failure(), want)
		previous = failed
	}
}

func TestRecoverableErrorDoesNotFailPipeline(t *testing.T) {
	sink := newChainPlugin(plugin.PluginTypeOutput, "sink", nil)
	p := pipeline.New()
	p.AddOutput(sink)
	require.NoError(t, p.Start())
	defer func() { require.NoError(t, p.Stop()) }()
	want := errors.New("one event rejected")
	require.True(t, sink.SendError(want))
	select {
	case got := <-p.ErrorChan():
		require.ErrorIs(t, got, want)
	case <-time.After(time.Second):
		t.Fatal("recoverable error missing")
	}
	require.True(t, p.IsRunning())
	require.NoError(t, p.Failure())
	select {
	case <-p.Failed():
		t.Fatal("recoverable error failed pipeline")
	default:
	}
}

func TestFailureWhileLaterPluginStartsRollsBack(t *testing.T) {
	sink := newChainPlugin(plugin.PluginTypeOutput, "sink", nil)
	source := newChainPlugin(plugin.PluginTypeInput, "source", nil)
	want := errors.New("sink failed during startup")
	source.setup = func(ctx context.Context) error { sink.Fail(want); <-ctx.Done(); return ctx.Err() }
	p := pipeline.New()
	p.AddInput(source)
	p.AddOutput(sink)
	err := p.Start()
	require.NotNil(t, err)
	require.ErrorIs(t, err, want)
	require.Equal(t, 1, strings.Count(err.Error(), want.Error()))
	require.ErrorIs(t, p.Failure(), want)
	require.Equal(t, 1, sink.stops)
	require.False(t, p.IsRunning())
	require.NoError(t, p.Stop())
}

func TestTerminalFailureRacesStopAndRestart(t *testing.T) {
	for range 50 {
		sink := newChainPlugin(plugin.PluginTypeOutput, "sink", nil)
		p := pipeline.New()
		p.AddOutput(sink)
		require.NoError(t, p.Start())
		var workers sync.WaitGroup
		for range 4 {
			workers.Go(func() { sink.Fail(errors.New("terminal")) })
		}
		require.NoError(t, p.Stop())
		workers.Wait()
		require.NoError(t, p.Start())
		require.True(t, p.IsRunning())
		require.NoError(t, p.Failure())
		select {
		case <-p.Failed():
			t.Fatal("previous failure leaked into the new run")
		default:
		}
		require.NoError(t, p.Stop())
	}
}

type missingFailureSignal struct{ *chainPlugin }

func (*missingFailureSignal) Failed() <-chan struct{} { return nil }

func TestMissingFailureSignalRollsBackStartup(t *testing.T) {
	sink := &missingFailureSignal{
		newChainPlugin(plugin.PluginTypeOutput, "sink", nil),
	}
	source := newChainPlugin(plugin.PluginTypeInput, "source", nil)
	p := pipeline.New()
	p.AddInput(source)
	p.AddOutput(sink)
	require.ErrorContains(t, p.Start(), "nil failure signal")
	require.Equal(t, 1, sink.stops)
	require.Zero(t, source.starts)
	require.False(t, p.IsRunning())
	require.NoError(t, p.Stop())
	require.Equal(t, 1, sink.stops)
}
