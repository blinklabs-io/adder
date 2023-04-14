package plugin

import (
	"flag"
	"fmt"
)

type PluginType int

const (
	PluginTypeInput  PluginType = 1
	PluginTypeOutput PluginType = 2
)

func PluginTypeName(pluginType PluginType) string {
	switch pluginType {
	case PluginTypeInput:
		return "input"
	case PluginTypeOutput:
		return "output"
	default:
		return ""
	}
}

type PluginEntry struct {
	Type               PluginType
	Name               string
	Options            []PluginOption
	NewFromOptionsFunc func() Plugin
}

var pluginEntries []PluginEntry

func Register(pluginEntry PluginEntry) {
	pluginEntries = append(pluginEntries, pluginEntry)
}

func PopulateCmdlineOptions(fs *flag.FlagSet) error {
	for _, plugin := range pluginEntries {
		flagPrefix := fmt.Sprintf("%s-%s-", PluginTypeName(plugin.Type), plugin.Name)
		for _, option := range plugin.Options {
			if err := option.AddToFlagSet(fs, flagPrefix); err != nil {
				return err
			}
		}
	}
	return nil
}

func ProcessEnvVars() error {
	for _, plugin := range pluginEntries {
		// Generate env var prefix based on plugin type and name
		envVarPrefix := fmt.Sprintf("%s-%s-", PluginTypeName(plugin.Type), plugin.Name)
		for _, option := range plugin.Options {
			if err := option.ProcessEnvVars(envVarPrefix); err != nil {
				return err
			}
		}
	}
	return nil
}

func ProcessConfig(pluginConfig map[string]map[string]map[interface{}]interface{}) error {
	for _, plugin := range pluginEntries {
		if pluginTypeData, ok := pluginConfig[PluginTypeName(plugin.Type)]; ok {
			if pluginData, ok := pluginTypeData[plugin.Name]; ok {
				for _, option := range plugin.Options {
					if err := option.ProcessConfig(pluginData); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func GetPlugins(pluginType PluginType) []string {
	ret := []string{}
	for _, plugin := range pluginEntries {
		if plugin.Type == pluginType {
			ret = append(ret, plugin.Name)
		}
	}
	return ret
}

func GetPlugin(pluginType PluginType, name string) Plugin {
	for _, plugin := range pluginEntries {
		if plugin.Type == pluginType {
			if plugin.Name == name {
				return plugin.NewFromOptionsFunc()
			}
		}
	}
	return nil
}
