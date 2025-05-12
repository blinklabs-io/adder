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

package config

import (
	"flag"
	"fmt"
	"os"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
)

const (
	DefaultInputPlugin  = "chainsync"
	DefaultOutputPlugin = "log"
)

type Config struct {
	Api        ApiConfig                         `yaml:"api"`
	ConfigFile string                            `yaml:"-"`
	Version    bool                              `yaml:"-"`
	Logging    LoggingConfig                     `yaml:"logging"`
	Debug      DebugConfig                       `yaml:"debug"`
	Input      string                            `yaml:"input"   envconfig:"INPUT"`
	Output     string                            `yaml:"output"  envconfig:"OUTPUT"`
	Plugin     map[string]map[string]map[any]any `yaml:"plugins"`
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
	Input:  DefaultInputPlugin,
	Output: DefaultOutputPlugin,
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
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", c)
	if err != nil {
		return fmt.Errorf("error processing environment: %w", err)
	}
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
