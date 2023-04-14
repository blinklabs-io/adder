package log

import (
	"github.com/blinklabs-io/snek/plugin"
)

var cmdlineOptions struct {
	level string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeOutput,
			Name:               "log",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "level",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the log level to use",
					DefaultValue: "info",
					Dest:         &(cmdlineOptions.level),
				},
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	p := New(
		WithLevel(cmdlineOptions.level),
	)
	return p
}
