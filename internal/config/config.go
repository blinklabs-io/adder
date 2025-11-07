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
	"flag"
	"fmt"
	"os"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/kelseyhightower/envconfig"
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

// populateByronGenesis sets defaults and validates ByronGenesisConfig
func (c *Config) populateByronGenesis() {
	cfg := &c.ByronGenesis
	// Apply defaults only when values are truly unset. Zero values are valid for Byron-less networks.
	if cfg.EpochLength == 0 {
		cfg.EpochLength = 21600
	}
	if cfg.ByronSlotsPerEpoch == 0 {
		cfg.ByronSlotsPerEpoch = cfg.EpochLength
	}
	if cfg.EndSlot == nil {
		defaultEndSlot := uint64(4492799)
		cfg.EndSlot = &defaultEndSlot
	}
}

// populateShelleyTransEpoch sets the Shelley transition epoch
func (c *Config) populateShelleyTransEpoch() {
	if c.ShelleyTransEpoch == -1 {
		c.ShelleyTransEpoch = 208 // Default to mainnet
	}
}

// populateShelleyGenesis sets defaults and validates ShelleyGenesisConfig
func (c *Config) populateShelleyGenesis() {
	cfg := &c.ShelleyGenesis
	if cfg.EpochLength == 0 {
		cfg.EpochLength = 432000
	}
}

const (
	DefaultInputPlugin  = "chainsync"
	DefaultOutputPlugin = "log"
)

type Config struct {
	ByronGenesis      ByronGenesisConfig                `yaml:"byron_genesis"      envconfig:"BYRON_GENESIS"`
	Plugin            map[string]map[string]map[any]any `yaml:"plugins"            envconfig:"PLUGINS"`
	Logging           LoggingConfig                     `yaml:"logging"            envconfig:"LOGGING"`
	ConfigFile        string                            `yaml:"-"`
	Input             string                            `yaml:"input"              envconfig:"INPUT"`
	Output            string                            `yaml:"output"             envconfig:"OUTPUT"`
	KupoUrl           string                            `yaml:"kupo_url"           envconfig:"KUPO_URL"`
	Api               ApiConfig                         `yaml:"api"                envconfig:"API"`
	Debug             DebugConfig                       `yaml:"debug"              envconfig:"DEBUG"`
	ShelleyGenesis    ShelleyGenesisConfig              `yaml:"shelley_genesis"    envconfig:"SHELLEY_GENESIS"`
	ShelleyTransEpoch int32                             `yaml:"shelley_trans_epoch" envconfig:"SHELLEY_TRANS_EPOCH"`
	Version           bool                              `yaml:"-"`
}

type ApiConfig struct {
	ListenAddress string `yaml:"address" envconfig:"API_ADDRESS"`
	ListenPort    uint   `yaml:"port"    envconfig:"API_PORT"`
}

type LoggingConfig struct {
	Level string `yaml:"level" envconfig:"LOGGING_LEVEL"`
}

type DebugConfig struct {
	ListenAddress string `yaml:"address" envconfig:"DEBUG_ADDRESS"`
	ListenPort    uint   `yaml:"port"    envconfig:"DEBUG_PORT"`
}

// Singleton config instance with default values
var globalConfig = &Config{
	Api: ApiConfig{
		ListenAddress: "0.0.0.0",
		ListenPort:    8080,
	},
	Logging: LoggingConfig{
		Level: "info",
	},
	Debug: DebugConfig{
		ListenAddress: "localhost",
		ListenPort:    0,
	},
	Input:             DefaultInputPlugin,
	Output:            DefaultOutputPlugin,
	KupoUrl:           "",
	ShelleyTransEpoch: -1, // Use -1 to indicate unset, will be populated later
	ByronGenesis: ByronGenesisConfig{
		EpochLength: 21600,
		EndSlot:     func() *uint64 { v := uint64(4492799); return &v }(),
	},
	ShelleyGenesis: ShelleyGenesisConfig{
		EpochLength: 432000,
	},
}

func (c *Config) Load(configFile string) error {
	// Load config file as YAML if provided
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return fmt.Errorf("error reading config file: %w", err)
		}
		err = yaml.Unmarshal(buf, c)
		if err != nil {
			return fmt.Errorf("error parsing config file: %w", err)
		}
	}
	// Load config values from environment variables
	err := envconfig.Process("dummy", c)
	if err != nil {
		return fmt.Errorf("error processing environment: %w", err)
	}
	// Populate Byron and Shelley genesis configs and transition epoch
	c.populateByronGenesis()
	c.populateShelleyGenesis()
	c.populateShelleyTransEpoch()
	return nil
}

func (c *Config) ParseCmdlineArgs(programName string, args []string) error {
	fs := flag.NewFlagSet(programName, flag.ExitOnError)
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
	if err := plugin.PopulateCmdlineOptions(fs); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	return nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}
