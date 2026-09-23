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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type chainPlugin struct {
	plugin.Base
	role   plugin.PluginType
	cfg    plugin.BaseConfig
	name   string
	order  *[]string
	setup  func(context.Context) error
	starts int
	stops  int
}

func (p *chainPlugin) Role() plugin.PluginType { return p.role }

func (p *chainPlugin) Start() error { return p.StartContext(context.Background()) }
func (p *chainPlugin) StartContext(ctx context.Context) error {
	return p.StartRun(ctx, p.cfg, func(ctx context.Context) error {
		p.starts++
		if p.order != nil {
			*p.order = append(*p.order, "start "+p.name)
		}
		if p.setup != nil {
			return p.setup(ctx)
		}
		return nil
	}, p.hooks())
}

func (p *chainPlugin) hooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{AfterWait: func() error {
		p.stops++
		if p.order != nil {
			*p.order = append(*p.order, "stop "+p.name)
		}
		return nil
	}}
}
func (p *chainPlugin) Stop() error { return p.Shutdown(p.hooks()) }

func newChainPlugin(
	role plugin.PluginType,
	name string,
	order *[]string,
) *chainPlugin {
	return &chainPlugin{
		role: role, name: name, order: order,
		cfg: plugin.BaseConfig{
			HasInput:  role != plugin.PluginTypeInput,
			HasOutput: role != plugin.PluginTypeOutput,
		},
	}
}

func TestChainStartsConsumersFirstAndPreservesEventOrder(t *testing.T) {
	var order []string
	sink := newChainPlugin(plugin.PluginTypeOutput, "sink", &order)
	second := newChainPlugin(plugin.PluginTypeFilter, "second", &order)
	first := newChainPlugin(plugin.PluginTypeFilter, "first", &order)
	source := newChainPlugin(plugin.PluginTypeInput, "source", &order)
	received := make(chan event.Event, 100)
	sink.setup = func(context.Context) error {
		sink.Go(func() {
			for {
				select {
				case <-sink.Done():
					return
				case evt := <-sink.Input():
					received <- evt
				}
			}
		})
		return nil
	}
	for _, pair := range []struct{ current, downstream *chainPlugin }{
		{second, sink}, {first, second},
	} {
		pair.current.setup = func(context.Context) error {
			if !pair.downstream.Running() {
				return errors.New("downstream not ready")
			}
			pair.current.Go(func() {
				for {
					select {
					case <-pair.current.Done():
						return
					case evt := <-pair.current.Input():
						evt.Type += "." + pair.current.name
						if !pair.current.Emit(evt) {
							return
						}
					}
				}
			})
			return nil
		}
	}
	source.setup = func(context.Context) error {
		if !first.Running() {
			return errors.New("first filter not ready")
		}
		source.Go(func() {
			for i := range 100 {
				if !source.Emit(event.Event{Type: fmt.Sprint(i)}) {
					return
				}
			}
		})
		return nil
	}
	p := pipeline.New()
	p.AddInput(source)
	p.AddFilter(first)
	p.AddFilter(second)
	p.AddOutput(sink)
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	for range 2 {
		order = nil
		require.NoError(t, p.Start())
		require.Equal(
			t,
			[]string{
				"start sink",
				"start second",
				"start first",
				"start source",
			},
			order,
		)
		for i := range 100 {
			select {
			case evt := <-received:
				require.Equal(t, fmt.Sprintf("%d.first.second", i), evt.Type)
			case <-time.After(3 * time.Second):
				t.Fatal("chain did not deliver startup burst")
			}
		}
		require.NoError(t, p.Stop())
		require.Equal(t, []string{
			"start sink", "start second", "start first", "start source",
			"stop source", "stop first", "stop second", "stop sink",
		}, order)
	}
}

func TestTopologyRejectedBeforeAnyPluginStarts(t *testing.T) {
	for _, scenario := range []string{"nil", "typed nil", "wrong role", "duplicate", "already running"} {
		t.Run(scenario, func(t *testing.T) {
			p := pipeline.New()
			sink := newChainPlugin(plugin.PluginTypeOutput, "sink", nil)
			p.AddOutput(sink)
			switch scenario {
			case "nil":
				p.AddInput(nil)
			case "typed nil":
				var source *chainPlugin
				p.AddInput(source)
			case "wrong role":
				p.AddFilter(
					newChainPlugin(plugin.PluginTypeOutput, "wrong", nil),
				)
			case "duplicate":
				p.AddOutput(sink)
			case "already running":
				source := newChainPlugin(
					plugin.PluginTypeInput,
					"owned elsewhere",
					nil,
				)
				require.NoError(t, source.Start())
				t.Cleanup(func() { require.NoError(t, source.Stop()) })
				p.AddInput(source)
				require.ErrorContains(t, p.Start(), "already running")
				require.NoError(t, p.Stop())
				require.True(
					t,
					source.Running(),
					"pipeline must not stop a plugin it did not start",
				)
				require.Zero(t, sink.starts)
				require.Zero(t, sink.stops)
				return
			}
			require.Error(t, p.Start())
			require.Zero(t, sink.starts)
			require.NoError(t, p.Stop())
			require.Zero(t, sink.stops)
		})
	}
}

func TestInvalidPortsRollBackStartedPlugins(t *testing.T) {
	for _, role := range []plugin.PluginType{plugin.PluginTypeInput, plugin.PluginTypeFilter, plugin.PluginTypeOutput} {
		t.Run(plugin.PluginTypeName(role), func(t *testing.T) {
			p := pipeline.New()
			ready := newChainPlugin(plugin.PluginTypeOutput, "ready", nil)
			p.AddOutput(ready)
			invalid := newChainPlugin(role, "invalid", nil)
			invalid.cfg = plugin.BaseConfig{}
			switch role {
			case plugin.PluginTypeInput:
				p.AddInput(invalid)
			case plugin.PluginTypeFilter:
				p.AddFilter(invalid)
			case plugin.PluginTypeOutput:
				p.AddOutput(invalid)
			}
			require.ErrorContains(t, p.Start(), "required")
			require.Equal(t, 1, invalid.starts)
			require.Equal(t, 1, invalid.stops)
			require.Equal(t, 1, ready.stops)
			require.False(t, p.IsRunning())
			require.Nil(t, invalid.ErrorChan())
			require.NoError(t, p.Stop())
			require.Equal(t, 1, invalid.stops)
		})
	}
}

func TestDeclaredPortsMustMatchRole(t *testing.T) {
	for _, role := range []plugin.PluginType{plugin.PluginTypeInput, plugin.PluginTypeOutput} {
		t.Run(plugin.PluginTypeName(role), func(t *testing.T) {
			component := newChainPlugin(role, "invalid", nil)
			component.cfg = plugin.BaseConfig{HasInput: true, HasOutput: true}
			p := pipeline.New()
			if role == plugin.PluginTypeInput {
				p.AddInput(component)
			} else {
				p.AddOutput(component)
			}
			require.ErrorContains(t, p.Start(), "must not expose")
			require.Equal(t, 1, component.stops)
		})
	}
}

func TestChainFailureRollsBackInReverseStartupOrder(t *testing.T) {
	var order []string
	sink := newChainPlugin(plugin.PluginTypeOutput, "sink", &order)
	downstream := newChainPlugin(plugin.PluginTypeFilter, "downstream", &order)
	failing := newChainPlugin(plugin.PluginTypeFilter, "failing", &order)
	failure := errors.New("setup failed")
	failing.setup = func(context.Context) error { return failure }
	source := newChainPlugin(plugin.PluginTypeInput, "source", &order)
	p := pipeline.New()
	p.AddInput(source)
	p.AddFilter(failing)
	p.AddFilter(downstream)
	p.AddOutput(sink)
	require.ErrorIs(t, p.Start(), failure)
	require.Equal(t, []string{
		"start sink", "start downstream", "start failing",
		"stop failing", "stop downstream", "stop sink",
	}, order)
	require.Zero(t, source.starts)
	require.NoError(t, p.Stop())
	require.Zero(t, source.stops)
}

func TestChainWithoutConsumersKeepsProducing(t *testing.T) {
	for _, filterCount := range []int{0, 2} {
		t.Run(fmt.Sprintf("filters=%d", filterCount), func(t *testing.T) {
			p := pipeline.New()
			source := newChainPlugin(plugin.PluginTypeInput, "source", nil)
			p.AddInput(source)
			for i := range filterCount {
				filter := newChainPlugin(
					plugin.PluginTypeFilter,
					fmt.Sprint(i),
					nil,
				)
				filter.setup = func(context.Context) error {
					filter.Go(func() {
						for {
							select {
							case <-filter.Done():
								return
							case evt := <-filter.Input():
								if !filter.Emit(evt) {
									return
								}
							}
						}
					})
					return nil
				}
				p.AddFilter(filter)
			}
			t.Cleanup(func() { require.NoError(t, p.Stop()) })
			for range 2 {
				produced := make(chan error, 1)
				source.setup = func(context.Context) error {
					source.Go(func() {
						for i := range 10000 {
							if !source.Emit(event.Event{Type: fmt.Sprint(i)}) {
								produced <- errors.New("source stopped before burst completed")
								return
							}
						}
						produced <- nil
					})
					return nil
				}
				require.NoError(t, p.Start())
				require.NoError(t, awaitResult(t, produced))
				require.True(t, p.IsRunning())
				require.True(t, source.Running())
				require.NoError(t, p.Stop())
			}
		})
	}
}
