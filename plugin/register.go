// Copyright 2023 Blink Labs Software
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

package plugin

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/pflag"
)

type PluginType int

const (
	PluginTypeInput  PluginType = 1
	PluginTypeOutput PluginType = 2
	PluginTypeFilter PluginType = 3
)

func PluginTypeName(pluginType PluginType) string {
	switch pluginType {
	case PluginTypeInput:
		return "input"
	case PluginTypeOutput:
		return "output"
	case PluginTypeFilter:
		return "filter"
	default:
		return ""
	}
}

// PluginEntry declares a factory and its configuration schema.
// Factories must validate semantic constraints and return errors without starting workers.
type PluginEntry struct {
	NewFromOptionsFunc func(Options) (ManagedPlugin, error)
	Name               string
	Description        string
	Options            []PluginOption
	Type               PluginType
}

var (
	registryMu    sync.RWMutex
	pluginEntries []PluginEntry
)

// Register publishes a definition. Invalid or duplicate definitions are programmer errors.
func Register(entry PluginEntry) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if PluginTypeName(entry.Type) == "" || entry.Name == "" ||
		entry.NewFromOptionsFunc == nil {
		panic("invalid plugin definition")
	}
	for _, existing := range pluginEntries {
		if existing.Type == entry.Type && existing.Name == entry.Name {
			panic("duplicate plugin definition: " + entry.Name)
		}
	}
	seen := make(map[string]bool)
	for _, option := range entry.Options {
		if option.Name == "" || seen[option.Name] {
			panic("invalid or duplicate option definition")
		}
		if _, err := option.normalize(option.DefaultValue); err != nil {
			panic(err)
		}
		seen[option.Name] = true
	}
	entry.Options = slices.Clone(entry.Options)
	pluginEntries = append(pluginEntries, entry)
}

func entries() []PluginEntry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	result := slices.Clone(pluginEntries)
	for i := range result {
		result[i].Options = slices.Clone(result[i].Options)
	}
	return result
}

func PopulateCmdlineOptions(fs *pflag.FlagSet) error {
	for _, entry := range entries() {
		for _, option := range entry.Options {
			if err := option.AddToFlagSet(fs, PluginTypeName(entry.Type), entry.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func GetPlugins(kind PluginType) []PluginEntry {
	var result []PluginEntry
	for _, entry := range entries() {
		if entry.Type == kind {
			result = append(result, entry)
		}
	}
	return result
}

// New constructs an independent instance from defaults and explicit values.
// It does not read process environment or command-line flags.
func (e PluginEntry) New(values map[string]any) (ManagedPlugin, error) {
	options, err := e.resolve(values, nil, nil)
	if err != nil {
		return nil, err
	}
	p, err := e.NewFromOptionsFunc(options)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", PluginTypeName(e.Type), e.Name, err)
	}
	if p == nil {
		return nil, fmt.Errorf(
			"%s.%s: factory returned nil",
			PluginTypeName(e.Type),
			e.Name,
		)
	}
	return p, nil
}

// GetPlugin constructs a registered plugin with instance-owned configuration.
func GetPlugin(
	kind PluginType,
	name string,
	values map[string]any,
) (ManagedPlugin, error) {
	for _, entry := range GetPlugins(kind) {
		if entry.Name == name {
			return entry.New(values)
		}
	}
	return nil, fmt.Errorf("unknown %s plugin %q", PluginTypeName(kind), name)
}

type configuredPlugin struct {
	entry  PluginEntry
	values Options
}

// Configuration is an immutable snapshot of resolved plugin options.
type Configuration struct{ plugins map[string]configuredPlugin }

// Options returns resolved scalar values without rereading flags or environment.
// The returned value exposes no mutable option storage.
func (c *Configuration) Options(kind PluginType, name string) (Options, error) {
	item, ok := c.plugins[PluginTypeName(kind)+"."+name]
	if !ok {
		return Options{}, fmt.Errorf("unknown %s plugin %q", PluginTypeName(kind), name)
	}
	return item.values, nil
}

// New constructs an instance using this snapshot, without rereading flags or environment.
func (c *Configuration) New(
	kind PluginType,
	name string,
) (ManagedPlugin, error) {
	key := PluginTypeName(kind) + "." + name
	item, ok := c.plugins[key]
	if !ok {
		return nil, fmt.Errorf(
			"unknown %s plugin %q",
			PluginTypeName(kind),
			name,
		)
	}
	p, err := item.entry.NewFromOptionsFunc(item.values)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	if p == nil {
		return nil, fmt.Errorf("%s: factory returned nil", key)
	}
	return p, nil
}

// ResolveConfig validates all supplied keys and scalar values, then snapshots
// defaults < YAML < environment < explicitly changed flags. A nil lookup uses
// os.LookupEnv; supply a lookup returning false for hermetic resolution.
// Custom environment aliases override generated names at the environment tier.
// Plugin-specific semantic validation happens when Configuration.New is called.
func ResolveConfig(
	data map[string]map[string]map[string]any,
	fs *pflag.FlagSet,
	lookup func(string) (string, bool),
) (*Configuration, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	definitions := entries()
	known := make(map[string]bool)
	for _, entry := range definitions {
		known[PluginTypeName(entry.Type)+"."+entry.Name] = true
	}
	for kind, plugins := range data {
		if kind != "input" && kind != "output" && kind != "filter" {
			return nil, fmt.Errorf("unknown plugin type %q", kind)
		}
		for name := range plugins {
			if !known[kind+"."+name] {
				return nil, fmt.Errorf("unknown %s plugin %q", kind, name)
			}
		}
	}
	result := &Configuration{plugins: make(map[string]configuredPlugin)}
	for _, entry := range definitions {
		kind := PluginTypeName(entry.Type)
		options, err := entry.resolve(data[kind][entry.Name], fs, lookup)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", kind, entry.Name, err)
		}
		result.plugins[kind+"."+entry.Name] = configuredPlugin{entry, options}
	}
	return result, nil
}

func (e PluginEntry) resolve(
	data map[string]any,
	fs *pflag.FlagSet,
	lookup func(string) (string, bool),
) (Options, error) {
	values := make(map[string]any, len(e.Options))
	for _, option := range e.Options {
		values[option.Name] = option.DefaultValue
	}
	for key := range data {
		if _, ok := values[key]; !ok {
			return Options{}, fmt.Errorf("unknown option %q", key)
		}
	}
	for _, option := range e.Options {
		value, err := option.normalize(option.DefaultValue)
		if err != nil {
			return Options{}, err
		}
		if supplied, ok := data[option.Name]; ok {
			value, err = option.normalize(supplied)
		}
		if err != nil {
			return Options{}, err
		}
		if lookup != nil {
			env := strings.ToUpper(
				strings.ReplaceAll(
					PluginTypeName(e.Type)+"-"+e.Name+"-"+option.Name,
					"-",
					"_",
				),
			)
			for _, name := range []string{env, option.CustomEnvVar} {
				if name == "" {
					continue
				}
				if raw, ok := lookup(name); ok {
					value, err = option.parse(raw)
					if err != nil {
						return Options{}, fmt.Errorf(
							"environment %s: %w",
							name,
							err,
						)
					}
				}
			}
		}
		if fs != nil {
			flag := fs.Lookup(option.flagName(PluginTypeName(e.Type), e.Name))
			if flag != nil && flag.Changed {
				value, err = option.parse(flag.Value.String())
			}
			if err != nil {
				return Options{}, err
			}
		}
		values[option.Name] = value
	}
	return Options{values: values}, nil
}
