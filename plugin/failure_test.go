// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugin_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/require"
)

func TestFailureSurvivesFullErrorChannelAndResetsOnRestart(t *testing.T) {
	var base plugin.Base
	plugintest.StartBase(t, &base, plugin.BaseConfig{})
	t.Cleanup(
		func() { require.NoError(t, base.Shutdown(plugin.ShutdownHooks{})) },
	)
	for range plugin.ErrorBuffer {
		require.True(t, base.TrySendError(errors.New("recoverable")))
	}
	failed := base.Failed()
	want := errors.New("terminal")
	base.Fail(want)
	require.ErrorIs(t, base.Failure(), want)
	require.False(t, base.Running())
	require.ErrorIs(t, base.Context().Err(), context.Canceled)
	select {
	case <-failed:
	default:
		t.Fatal("full error buffer hid terminal failure")
	}
	base.Fail(errors.New("second failure"))
	require.ErrorIs(t, base.Failure(), want)
	require.ErrorIs(
		t,
		base.StartRun(
			context.Background(),
			plugin.BaseConfig{},
			func(context.Context) error { return nil },
			plugin.ShutdownHooks{},
		),
		want,
	)
	require.NoError(t, base.Shutdown(plugin.ShutdownHooks{}))
	require.ErrorIs(t, base.Failure(), want)
	plugintest.StartBase(t, &base, plugin.BaseConfig{})
	require.NoError(t, base.Failure())
	require.NotEqual(t, failed, base.Failed())
}

func TestFailureRacesShutdown(t *testing.T) {
	for range 100 {
		var base plugin.Base
		plugintest.StartBase(t, &base, plugin.BaseConfig{})
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() { base.Fail(errors.New("terminal")) })
		}
		require.NoError(t, base.Shutdown(plugin.ShutdownHooks{}))
		wg.Wait()
		require.False(t, base.Running())
		base.Fail(errors.New("late"))
		require.Nil(t, base.ErrorChan())
	}
}

func TestFailureDuringSetupRetainsCause(t *testing.T) {
	var base plugin.Base
	want := errors.New("setup worker failed")
	err := base.StartRun(
		context.Background(),
		plugin.BaseConfig{},
		func(context.Context) error { base.Fail(want); return base.Context().Err() },
		plugin.ShutdownHooks{},
	)
	require.ErrorIs(t, err, want)
	require.False(t, base.Running())
	require.Nil(t, base.ErrorChan())
	require.NoError(t, base.Shutdown(plugin.ShutdownHooks{}))
}
