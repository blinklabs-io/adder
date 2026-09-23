// Copyright 2025 Blink Labs Software
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

package pipeline

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/blinklabs-io/adder/plugin"
)

type startedPlugin struct {
	component plugin.ManagedPlugin
	role      plugin.PluginType
	index     int
}

func (p *Pipeline) validateTopology() error {
	seen := make(map[plugin.ManagedPlugin]string)
	groups := []struct {
		role       plugin.PluginType
		components []plugin.ManagedPlugin
	}{
		{plugin.PluginTypeInput, p.inputs},
		{plugin.PluginTypeFilter, p.filters},
		{plugin.PluginTypeOutput, p.outputs},
	}
	for _, group := range groups {
		for index, component := range group.components {
			label := fmt.Sprintf(
				"%s[%d]",
				plugin.PluginTypeName(group.role),
				index,
			)
			if component == nil {
				return fmt.Errorf("%s: nil plugin", label)
			}
			value := reflect.ValueOf(component)
			//nolint:exhaustive // IsNil is valid only for these kinds.
			switch value.Kind() {
			case reflect.Pointer,
				reflect.Interface,
				reflect.Chan,
				reflect.Func,
				reflect.Map,
				reflect.Slice:
				if value.IsNil() {
					return fmt.Errorf("%s: nil plugin", label)
				}
			}
			// Pointer plugins have instance identity. Value plugins
			// with non-comparable fields cannot be keys in this map.
			if value.Comparable() {
				if previous, exists := seen[component]; exists {
					return fmt.Errorf(
						"%s: plugin instance already used at %s",
						label,
						previous,
					)
				}
				seen[component] = label
			}
			if role := component.Role(); role != group.role {
				return fmt.Errorf(
					"%s: %T declares role %d, expected %s",
					label,
					component,
					role,
					plugin.PluginTypeName(group.role),
				)
			}
			if state, ok := component.(interface{ Running() bool }); ok &&
				state.Running() {
				return fmt.Errorf(
					"%s: plugin is already running; pipeline requires lifecycle ownership",
					label,
				)
			}
		}
	}
	return nil
}

func validatePorts(
	component plugin.ManagedPlugin,
	role plugin.PluginType,
) error {
	in, out := component.InputChan(), component.OutputChan()
	if (role == plugin.PluginTypeFilter || role == plugin.PluginTypeOutput) &&
		in == nil {
		return errors.New("required input channel is nil")
	}
	if (role == plugin.PluginTypeFilter || role == plugin.PluginTypeInput) &&
		out == nil {
		return errors.New("required output channel is nil")
	}
	if role == plugin.PluginTypeInput && in != nil {
		return errors.New("input plugin must not expose an input channel")
	}
	if role == plugin.PluginTypeOutput && out != nil {
		return errors.New("output plugin must not expose an output channel")
	}
	return nil
}

// stopPlugins requires lifecycleMu and all forwarding goroutines to have exited.
func (p *Pipeline) stopPlugins() []error {
	var errs []error
	for _, started := range slices.Backward(p.started) {
		if err := started.component.Stop(); err != nil {
			errs = append(
				errs,
				fmt.Errorf(
					"failed to stop %s[%d]: %w",
					plugin.PluginTypeName(started.role),
					started.index,
					err,
				),
			)
		}
	}
	p.started = nil
	return errs
}
