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

package pipeline_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/input/chainsync"
	nodefixture "github.com/blinklabs-io/adder/internal/plugintest"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type contextPlugin struct {
	plugin.Base
	role     plugin.PluginType
	setup    func(context.Context) error
	starts   atomic.Int32
	cleanups atomic.Int32
}

func (p *contextPlugin) Role() plugin.PluginType { return p.role }

func (p *contextPlugin) StartContext(ctx context.Context) error {
	return p.StartRun(
		ctx,
		plugin.BaseConfig{
			HasInput:  p.role != plugin.PluginTypeInput,
			HasOutput: p.role != plugin.PluginTypeOutput,
		},
		func(ctx context.Context) error {
			p.starts.Add(1)
			if p.setup != nil {
				return p.setup(ctx)
			}
			return nil
		},
		p.hooks(),
	)
}

func (p *contextPlugin) hooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{AfterWait: func() error {
		p.cleanups.Add(1)
		return nil
	}}
}

func (p *contextPlugin) Stop() error { return p.Shutdown(p.hooks()) }

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle operation did not finish")
		return nil
	}
}

func TestStopCancelsNodeHandshakeAndPipelineRestarts(t *testing.T) {
	testCanceledNodeHandshake(t, false)
}

func TestStartContextCancelsNodeHandshakeAndPipelineRestarts(t *testing.T) {
	testCanceledNodeHandshake(t, true)
}

func testCanceledNodeHandshake(t *testing.T, cancelParent bool) {
	t.Helper()
	listener, err := net.ListenTCP(
		"tcp",
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
	)
	require.NoError(t, err)
	defer listener.Close()
	require.NoError(t, listener.SetDeadline(time.Now().Add(5*time.Second)))
	source := chainsync.New(
		chainsync.WithAddress(listener.Addr().String()),
		chainsync.WithNtcTcp(true), chainsync.WithNetworkMagic(42),
	)
	earlier, later, sink := &contextPlugin{
		role: plugin.PluginTypeInput,
	}, &contextPlugin{
		role: plugin.PluginTypeInput,
	}, &contextPlugin{
		role: plugin.PluginTypeOutput,
	}
	p := pipeline.New()
	p.AddInput(earlier)
	p.AddInput(source)
	p.AddInput(later)
	p.AddOutput(sink)
	originalErrors := p.ErrorChan()
	started := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if cancelParent {
			started <- p.StartContext(ctx)
		} else {
			started <- p.Start()
		}
	}()
	peer, err := listener.Accept()
	require.NoError(t, err)
	defer peer.Close()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	handshake := make([]byte, 1)
	_, err = io.ReadFull(peer, handshake)
	require.NoError(t, err)

	stopped := make(chan error, 1)
	if cancelParent {
		cancel()
	} else {
		go func() { stopped <- p.Stop() }()
	}
	require.ErrorIs(t, awaitResult(t, started), context.Canceled)
	if !cancelParent {
		require.NoError(t, awaitResult(t, stopped))
	}
	require.False(t, p.IsRunning())
	require.False(t, source.Running())
	require.Nil(t, source.OutputChan())
	require.Equal(t, int32(1), earlier.cleanups.Load())
	require.Zero(t, later.starts.Load())
	require.Equal(t, int32(1), sink.cleanups.Load())
	_, open := <-originalErrors
	require.False(t, open)
	_, err = io.Copy(io.Discard, peer)
	require.NoError(t, err, "canceled setup must close its socket")

	chainsync.WithAddress(nodefixture.Node(t))(source)
	require.NoError(t, p.Start())
	require.True(t, p.IsRunning())
	require.NotEqual(t, originalErrors, p.ErrorChan())
	require.Equal(t, int32(2), earlier.starts.Load())
	require.Equal(t, int32(1), later.starts.Load())
	require.NoError(t, p.Stop())
	require.NoError(t, p.Stop())
	require.Equal(t, int32(2), earlier.cleanups.Load())
}

func TestStartContextAlreadyCanceledAcquiresNothing(t *testing.T) {
	sink := &contextPlugin{role: plugin.PluginTypeOutput}
	p := pipeline.New()
	p.AddOutput(sink)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, p.StartContext(ctx), context.Canceled)
	require.Zero(t, sink.starts.Load())
	require.Zero(t, sink.cleanups.Load())
	require.False(t, p.IsRunning())
	require.NoError(t, p.Start())
	require.NoError(t, p.Stop())
}

func TestStopCancelsStartupAtEveryStage(t *testing.T) {
	for _, stage := range []string{"input", "filter", "output"} {
		t.Run(stage, func(t *testing.T) {
			entered := make(chan struct{})
			blocker := &contextPlugin{setup: func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			}}
			earlier, later := &contextPlugin{
				role: plugin.PluginTypeOutput,
			}, &contextPlugin{
				role: plugin.PluginTypeInput,
			}
			p := pipeline.New()
			p.AddOutput(earlier)
			switch stage {
			case "input":
				blocker.role = plugin.PluginTypeInput
				p.AddInput(blocker)
			case "filter":
				blocker.role = plugin.PluginTypeFilter
				p.AddFilter(blocker)
			case "output":
				blocker.role = plugin.PluginTypeOutput
				p.AddOutput(blocker)
			}
			p.AddInput(later)
			started := make(chan error, 1)
			go func() { started <- p.Start() }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("setup did not start")
			}
			stopped := make(chan error, 8)
			for range 8 {
				go func() { stopped <- p.Stop() }()
			}
			require.ErrorIs(t, awaitResult(t, started), context.Canceled)
			for range 8 {
				require.NoError(t, awaitResult(t, stopped))
			}
			require.Equal(t, int32(1), blocker.cleanups.Load())
			require.Equal(t, int32(1), earlier.cleanups.Load())
			require.Zero(t, later.starts.Load())
			require.False(t, p.IsRunning())
		})
	}
}

// Startup can finish its last operation while cancellation arrives. The
// pipeline must still roll it back before publishing a running state.
type finishingStartup struct {
	*chainPlugin
	entered chan struct{}
	release chan struct{}
}

func (p *finishingStartup) StartContext(ctx context.Context) error {
	if err := p.chainPlugin.StartContext(ctx); err != nil {
		return err
	}
	close(p.entered)
	<-p.release
	return nil
}

func TestStopWaitsForFinishingStartupAndRollsItBack(t *testing.T) {
	first := &contextPlugin{role: plugin.PluginTypeOutput}
	last := &finishingStartup{
		chainPlugin: newChainPlugin(plugin.PluginTypeInput, "last", nil),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	p := pipeline.New()
	p.AddOutput(first)
	p.AddInput(last)
	started := make(chan error, 1)
	go func() { started <- p.Start() }()
	select {
	case <-last.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case <-first.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("pipeline did not cancel its run")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned before startup completed")
	default:
	}
	close(last.release)
	require.ErrorIs(t, awaitResult(t, started), context.Canceled)
	require.NoError(t, awaitResult(t, stopped))
	require.Equal(t, 1, last.stops)
	require.Equal(t, int32(1), first.cleanups.Load())
	require.False(t, p.IsRunning())
}
