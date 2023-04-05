package config

import (
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Logging LoggingConfig `yaml:"logging"`
	Debug   DebugConfig   `yaml:"debug"`
	Node    NodeConfig    `yaml:"node"`
}

type LoggingConfig struct {
	Level string `yaml:"level" envconfig:"LOGGING_LEVEL"`
}

type DebugConfig struct {
	ListenAddress string `yaml:"address" envconfig:"DEBUG_ADDRESS"`
	ListenPort    uint   `yaml:"port" envconfig:"DEBUG_PORT"`
}

type NodeConfig struct {
	Network      string `yaml:"network" envconfig:"CARDANO_NETWORK"`
	NetworkMagic uint32 `yaml:"networkMagic" envconfig:"CARDANO_NODE_NETWORK_MAGIC"`
	Address      string `yaml:"address" envconfig:"CARDANO_NODE_SOCKET_TCP_HOST"`
	Port         uint   `yaml:"port" envconfig:"CARDANO_NODE_SOCKET_TCP_PORT"`
	SocketPath   string `yaml:"socketPath" envconfig:"CARDANO_NODE_SOCKET_PATH"`
	UseNtN       bool   `yaml:"useNtN" envconfig:"CARDANO_NODE_USE_NTN"`
}

// Singleton config instance with default values
var globalConfig = &Config{
	Logging: LoggingConfig{
		Level: "info",
	},
	Debug: DebugConfig{
		ListenAddress: "localhost",
		ListenPort:    0,
	},
	Node: NodeConfig{
		Network:    "mainnet",
		SocketPath: "/node-ipc/node.socket",
	},
}

func Load(configFile string) (*Config, error) {
	// Load config file as YAML if provided
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("error reading config file: %s", err)
		}
		err = yaml.Unmarshal(buf, globalConfig)
		if err != nil {
			return nil, fmt.Errorf("error parsing config file: %s", err)
		}
	}
	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %s", err)
	}
	return globalConfig, nil
}

// Config returns the global config instance
func GetConfig() *Config {
	return globalConfig
}
