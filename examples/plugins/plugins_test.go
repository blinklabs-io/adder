// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugins_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/examples/plugins"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/require"
)

func TestLifecycleContracts(t *testing.T) {
	source, err := plugins.NewTickerInput(time.Hour)
	require.NoError(t, err)
	plugintest.Lifecycle(t, source)
	sink, err := plugins.NewHandlerOutput(
		func(context.Context, event.Event) error { return nil },
	)
	require.NoError(t, err)
	plugintest.Lifecycle(t, sink)
}

func TestExampleChainReportsTerminalFailure(t *testing.T) {
	want := errors.New("handler unavailable")
	ready := make(chan struct{})
	source, err := plugins.NewTickerInput(time.Millisecond)
	require.NoError(t, err)
	sink, err := plugins.NewHandlerOutput(
		func(context.Context, event.Event) error { <-ready; return want },
	)
	require.NoError(t, err)
	p := pipeline.New()
	p.AddInput(source)
	p.AddOutput(sink)
	require.NoError(t, p.Start())
	close(ready)
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	select {
	case <-p.Failed():
	case <-time.After(time.Second):
		t.Fatal("terminal failure was not propagated")
	}
	require.ErrorIs(t, p.Failure(), want)
	require.False(t, p.IsRunning())
	require.NoError(t, p.Stop())
	require.False(t, source.Running())
	require.False(t, sink.Running())
}
