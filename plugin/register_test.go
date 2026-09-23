// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package plugin_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	eventfilter "github.com/blinklabs-io/adder/filter/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestConfigurationSnapshots(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, plugin.PopulateCmdlineOptions(fs))
	require.NoError(t, fs.Parse([]string{"--filter-type=input.block"}))
	data := map[string]map[string]map[string]any{
		"filter": {"event": {"type": "input.transaction"}},
	}
	lookup := func(string) (string, bool) { return "input.rollback", true }
	first, err := plugin.ResolveConfig(data, fs, lookup)
	require.NoError(t, err)
	require.NoError(t, fs.Set("filter-type", "input.transaction"))
	second, err := plugin.ResolveConfig(data, fs, lookup)
	require.NoError(t, err)
	firstOptions, err := first.Options(plugin.PluginTypeFilter, "event")
	require.NoError(t, err)
	require.Equal(t, "input.block", firstOptions.String("type"))
	secondOptions, err := second.Options(plugin.PluginTypeFilter, "event")
	require.NoError(t, err)
	require.Equal(t, "input.transaction", secondOptions.String("type"))
	_, err = first.Options(plugin.PluginTypeFilter, "missing")
	require.ErrorContains(t, err, "unknown filter plugin")
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			a, err := first.New(plugin.PluginTypeFilter, "event")
			require.NoError(t, err)
			b, err := second.New(plugin.PluginTypeFilter, "event")
			require.NoError(t, err)
			require.IsType(t, &eventfilter.Event{}, a)
			require.NotSame(t, a, b)

			require.NoError(t, a.StartContext(context.Background()))
			require.NoError(t, b.StartContext(context.Background()))
			a.InputChan() <- event.Event{Type: "input.block"}
			b.InputChan() <- event.Event{Type: "input.transaction"}
			select {
			case ev := <-a.OutputChan():
				require.Equal(t, "input.block", ev.Type)
			case <-time.After(time.Second):
				t.Error("first snapshot lost CLI value")
			}
			select {
			case ev := <-b.OutputChan():
				require.Equal(t, "input.transaction", ev.Type)
			case <-time.After(time.Second):
				t.Error("second snapshot lost CLI value")
			}
			require.NoError(t, a.Stop())
			require.NoError(t, b.Stop())
		})
	}
	wg.Wait()
}

func TestRegistryStrictness(t *testing.T) {
	for _, data := range []map[string]map[string]map[string]any{
		{"typo": {}}, {"filter": {"typo": {}}}, {"filter": {"event": {"typo": true}}}, {"filter": {"event": {"type": false}}},
	} {
		_, err := plugin.ResolveConfig(
			data,
			nil,
			func(string) (string, bool) { return "", false },
		)
		require.Error(t, err)
	}
	_, err := plugin.GetPlugin(plugin.PluginTypeInput, "missing", nil)
	require.ErrorContains(t, err, "unknown input")
}

func TestRegistryDefinitionCopies(t *testing.T) {
	entries := plugin.GetPlugins(plugin.PluginTypeFilter)
	require.NotEmpty(t, entries)
	require.NotEmpty(t, entries[0].Options)
	entries[0].Options[0].DefaultValue = "changed"
	again := plugin.GetPlugins(plugin.PluginTypeFilter)
	require.NotEmpty(t, again)
	require.NotEmpty(t, again[0].Options)
	require.NotEqual(t, "changed", again[0].Options[0].DefaultValue)
}
