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

package config

import (
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/kelseyhightower/envconfig"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v2"
)

// ByronGenesisConfig holds Byron era genesis parameters
type ByronGenesisConfig struct {
	EndSlot            *uint64 `yaml:"end_slot"              envconfig:"BYRON_GENESIS_END_SLOT"`
	EpochLength        uint64  `yaml:"epoch_length"          envconfig:"BYRON_GENESIS_EPOCH_LENGTH"`
	ByronSlotsPerEpoch uint64  `yaml:"byron_slots_per_epoch" envconfig:"BYRON_GENESIS_BYRON_SLOTS_PER_EPOCH"`
}

// ShelleyGenesisConfig holds Shelley era genesis parameters
type ShelleyGenesisConfig struct {
	EpochLength uint64 `yaml:"epoch_length" envconfig:"SHELLEY_GENESIS_EPOCH_LENGTH"`
}

const (
	DefaultInputPlugin  = "chainsync"
	DefaultOutputPlugin = "log"
)

type Config struct {
	ByronGenesis      ByronGenesisConfig                   `yaml:"byron_genesis"       envconfig:"BYRON_GENESIS"`
	Plugin            map[string]map[string]map[string]any `yaml:"plugins"                                             ignored:"true"`
	ConfigFile        string                               `yaml:"-"                                                   ignored:"true"`
	Input             string                               `yaml:"input"               envconfig:"INPUT"`
	Output            string                               `yaml:"output"              envconfig:"OUTPUT"`
	KupoUrl           string                               `yaml:"kupo_url"            envconfig:"KUPO_URL"`
	Api               ApiConfig                            `yaml:"api"                 envconfig:"API"`
	Debug             DebugConfig                          `yaml:"debug"               envconfig:"DEBUG"`
	ShelleyGenesis    ShelleyGenesisConfig                 `yaml:"shelley_genesis"     envconfig:"SHELLEY_GENESIS"`
	ShelleyTransEpoch int32                                `yaml:"shelley_trans_epoch" envconfig:"SHELLEY_TRANS_EPOCH"`
	Version           bool                                 `yaml:"-"                                                   ignored:"true"`
}

type ApiConfig struct {
	ListenAddress string          `yaml:"address" envconfig:"API_ADDRESS"`
	ListenPort    uint            `yaml:"port"    envconfig:"API_PORT"`
	Events        ApiEventsConfig `yaml:"events"  envconfig:"API_EVENTS"`
}

type ApiEventsConfig struct {
	BufferSize uint `yaml:"buffer-size" envconfig:"API_EVENTS_BUFFER_SIZE"`
}

type DebugConfig struct {
	ListenAddress string `yaml:"address" envconfig:"DEBUG_ADDRESS"`
	ListenPort    uint   `yaml:"port"    envconfig:"DEBUG_PORT"`
}

// New returns independent application defaults.
func New() *Config {
	return &Config{
		Api: ApiConfig{
			ListenAddress: "0.0.0.0",
			ListenPort:    8080,
			Events: ApiEventsConfig{
				BufferSize: 100,
			},
		},
		Debug: DebugConfig{
			ListenAddress: "localhost",
			ListenPort:    0,
		},
		Input:             DefaultInputPlugin,
		Output:            DefaultOutputPlugin,
		KupoUrl:           "",
		ShelleyTransEpoch: 208,
		ByronGenesis: ByronGenesisConfig{
			EpochLength:        21600,
			ByronSlotsPerEpoch: 21600,
			EndSlot:            func() *uint64 { v := uint64(4492799); return &v }(),
		},
		ShelleyGenesis: ShelleyGenesisConfig{
			EpochLength: 432000,
		},
	}
}

var globalConfig = New()

// Load replaces c with freshly resolved defaults, YAML, and environment values.
// A failed load leaves c unchanged. Load is not concurrent with readers of c.
func (c *Config) Load(configFile string) error {
	return c.LoadWithFlags(configFile, nil)
}

// LoadWithFlags applies defaults < YAML < environment < explicit CLI flags.
// Flag storage is independent of c, so the same FlagSet can be reused on reload.
func (c *Config) LoadWithFlags(configFile string, fs *pflag.FlagSet) error {
	next := New()
	next.ConfigFile = configFile
	byronSlotsSet := false
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return fmt.Errorf("error reading config file: %w", err)
		}
		var fields struct {
			Byron struct {
				Slots *uint64 `yaml:"byron_slots_per_epoch"`
			} `yaml:"byron_genesis"`
		}
		if err := yaml.Unmarshal(buf, &fields); err == nil {
			byronSlotsSet = fields.Byron.Slots != nil
		}
		if err := yaml.UnmarshalStrict(buf, next); err != nil {
			return fmt.Errorf("error parsing config file: %w", err)
		}
	}
	if err := envconfig.Process("", next); err != nil {
		var parse *envconfig.ParseError
		if errors.As(err, &parse) {
			return fmt.Errorf(
				"error processing environment %s: invalid %s",
				parse.KeyName,
				parse.TypeName,
			)
		}
		return errors.New("error processing environment: invalid configuration")
	}
	if fs != nil {
		for name, dest := range map[string]*string{
			"config": &next.ConfigFile, "input": &next.Input, "output": &next.Output,
			"api-address":   &next.Api.ListenAddress,
			"debug-address": &next.Debug.ListenAddress,
		} {
			if fs.Changed(name) {
				value, err := fs.GetString(name)
				if err != nil {
					return err
				}
				*dest = value
			}
		}
		for name, dest := range map[string]*uint{"api-port": &next.Api.ListenPort, "debug-port": &next.Debug.ListenPort} {
			if fs.Changed(name) {
				value, err := fs.GetUint(name)
				if err != nil {
					return err
				}
				*dest = value
			}
		}
		if fs.Changed("version") {
			value, err := fs.GetBool("version")
			if err != nil {
				return err
			}
			next.Version = value
		}
	}
	if _, ok := os.LookupEnv("BYRON_GENESIS_BYRON_SLOTS_PER_EPOCH"); ok {
		byronSlotsSet = true
	}
	if !byronSlotsSet {
		next.ByronGenesis.ByronSlotsPerEpoch = next.ByronGenesis.EpochLength
	}
	if next.ShelleyTransEpoch < 0 {
		return errors.New("shelley_trans_epoch must be nonnegative")
	}
	if next.Api.ListenPort > 65535 || next.Debug.ListenPort > 65535 {
		return errors.New("API and debug ports must be between 0 and 65535")
	}
	*c = *next
	return nil
}

// BindFlags registers independent CLI storage initialized from standard defaults.
func (c *Config) BindFlags(fs *pflag.FlagSet) error {
	c = New()
	fs.StringVar(&c.ConfigFile, "config", "", "path to config file to load")
	fs.BoolVar(&c.Version, "version", false, "show version and exit")
	fs.StringVar(
		&c.Input,
		"input",
		DefaultInputPlugin,
		"input plugin to use, 'list' to show available",
	)
	fs.StringVar(
		&c.Output,
		"output",
		DefaultOutputPlugin,
		"output plugin to use, 'list' to show available",
	)
	fs.StringVar(
		&c.Api.ListenAddress,
		"api-address",
		c.Api.ListenAddress,
		"API listen address",
	)
	fs.UintVar(
		&c.Api.ListenPort,
		"api-port",
		c.Api.ListenPort,
		"API listen port",
	)
	fs.StringVar(
		&c.Debug.ListenAddress,
		"debug-address",
		c.Debug.ListenAddress,
		"debug listener address",
	)
	fs.UintVar(
		&c.Debug.ListenPort,
		"debug-port",
		c.Debug.ListenPort,
		"debug listener port (0 to disable)",
	)
	return plugin.PopulateCmdlineOptions(fs)
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}

// ResolvePlugins snapshots plugin configuration. The top-level Kupo URL supplies
// a fallback only when an input's YAML does not explicitly provide kupo-url.
func (c *Config) ResolvePlugins(
	fs *pflag.FlagSet,
) (*plugin.Configuration, error) {
	data := make(map[string]map[string]map[string]any, len(c.Plugin))
	for kind, entries := range c.Plugin {
		data[kind] = make(map[string]map[string]any, len(entries))
		for name, values := range entries {
			data[kind][name] = maps.Clone(values)
		}
	}
	if data["input"] == nil {
		data["input"] = make(map[string]map[string]any)
	}
	for _, entry := range plugin.GetPlugins(plugin.PluginTypeInput) {
		if entry.Name != "chainsync" && entry.Name != "mempool" {
			continue
		}
		if data["input"][entry.Name] == nil {
			data["input"][entry.Name] = make(map[string]any)
		}
		if _, set := data["input"][entry.Name]["kupo-url"]; !set {
			data["input"][entry.Name]["kupo-url"] = c.KupoUrl
		}
	}
	return plugin.ResolveConfig(data, fs, nil)
}
