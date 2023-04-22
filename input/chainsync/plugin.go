package chainsync

import (
	"github.com/blinklabs-io/snek/plugin"
)

var cmdlineOptions struct {
	network        string
	networkMagic   uint
	address        string
	socketPath     string
	ntcTcp         bool
	intersectTip   bool
	intersectPoint string
}

func init() {
	plugin.Register(
		plugin.PluginEntry{
			Type:               plugin.PluginTypeInput,
			Name:               "chainsync",
			NewFromOptionsFunc: NewFromCmdlineOptions,
			Options: []plugin.PluginOption{
				{
					Name:         "network",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NETWORK",
					Description:  "specifies a well-known Cardano network name",
					DefaultValue: "mainnet",
					Dest:         &(cmdlineOptions.network),
				},
				{
					Name:         "network-magic",
					Type:         plugin.PluginOptionTypeUint,
					Description:  "specifies the network magic value to use, overrides 'network'",
					DefaultValue: uint(0),
					Dest:         &(cmdlineOptions.networkMagic),
				},
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					Description:  "specifies the TCP address of the node to connect to in the form 'host:port'",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.address),
				},
				{
					Name:         "socket-path",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NODE_SOCKET_PATH",
					Description:  "specifies the path to the UNIX socket to connect to",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.socketPath),
				},
				{
					Name:         "ntc-tcp",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "use the NtC (node-to-client) protocol over TCP, for use when exposing a node's UNIX socket via socat or similar",
					DefaultValue: false,
					Dest:         &(cmdlineOptions.ntcTcp),
				},
				{
					Name:         "intersect-tip",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "start syncing at the chain tip (defaults to chain genesis)",
					DefaultValue: true,
					Dest:         &(cmdlineOptions.intersectTip),
				},
				// TODO: intersect-point
			},
		},
	)
}

func NewFromCmdlineOptions() plugin.Plugin {
	p := New(
		WithNetwork(cmdlineOptions.network),
		WithNetworkMagic(uint32(cmdlineOptions.networkMagic)),
		WithAddress(cmdlineOptions.address),
		WithSocketPath(cmdlineOptions.socketPath),
		WithNtcTcp(cmdlineOptions.ntcTcp),
		WithIntersectTip(cmdlineOptions.intersectTip),
		// TODO: WithIntersectPoints
	)
	return p
}
