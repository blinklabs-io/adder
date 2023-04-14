package chainsync

import (
	"github.com/blinklabs-io/snek/plugin"
)

var cmdlineOptions struct {
	network        string
	networkMagic   uint
	address        string
	port           uint
	socketPath     string
	ntn            bool
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
					CustomEnvVar: "CARDANO_NODE_NETWORK_MAGIC",
					Description:  "specifies the network magic value to use, overrides 'network'",
					DefaultValue: uint(0),
					Dest:         &(cmdlineOptions.networkMagic),
				},
				{
					Name:         "address",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NODE_SOCKET_TCP_HOST",
					Description:  "specifies the TCP address of the node to connect to",
					DefaultValue: "",
					Dest:         &(cmdlineOptions.address),
				},
				{
					Name:         "port",
					Type:         plugin.PluginOptionTypeUint,
					CustomEnvVar: "CARDANO_NODE_SOCKET_TCP_PORT",
					Description:  "specifies the TCP port of the node to connect to",
					DefaultValue: uint(0),
					Dest:         &(cmdlineOptions.port),
				},
				{
					Name:         "socket-path",
					Type:         plugin.PluginOptionTypeString,
					CustomEnvVar: "CARDANO_NODE_SOCKET_PATH",
					Description:  "specifies the path to the UNIX socket to connect to",
					DefaultValue: "/node-ipc/node.socket",
					Dest:         &(cmdlineOptions.socketPath),
				},
				{
					Name:         "use-ntn",
					Type:         plugin.PluginOptionTypeBool,
					CustomEnvVar: "CARDANO_NODE_USE_NTN",
					Description:  "specifies that node-to-node mode should be used (defaults node-to-client)",
					DefaultValue: false,
					Dest:         &(cmdlineOptions.ntn),
				},
				{
					Name:         "intersect-tip",
					Type:         plugin.PluginOptionTypeBool,
					Description:  "start syncing at the chain tip (defaults to chain genesis)",
					DefaultValue: false,
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
		WithPort(cmdlineOptions.port),
		WithSocketPath(cmdlineOptions.socketPath),
		WithNodeToNode(cmdlineOptions.ntn),
		WithIntersectTip(cmdlineOptions.intersectTip),
		// TODO: WithIntersectPoints
	)
	return p
}
