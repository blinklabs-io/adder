package chainsync

import (
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

type ChainSyncOptionFunc func(*ChainSync)

// WithNetwork specifies the network
func WithNetwork(network string) ChainSyncOptionFunc {
	return func(o *ChainSync) {
		o.network = network
	}
}

// WithNetworkMagic specifies the network magic value
func WithNetworkMagic(networkMagic uint32) ChainSyncOptionFunc {
	return func(o *ChainSync) {
		o.networkMagic = networkMagic
	}
}

// WithNtcTcp specifies whether to use the NtC (node-to-client) protocol over TCP. This is useful when exposing a node's UNIX socket via socat or similar. The default is to use the NtN (node-to-node) protocol over TCP
func WithNtcTcp(ntcTcp bool) ChainSyncOptionFunc {
	return func(o *ChainSync) {
		o.ntcTcp = ntcTcp
	}
}

// WithSocketPath specifies the socket path of the node to connect to
func WithSocketPath(socketPath string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.socketPath = socketPath
	}
}

// WithAddress specifies the TCP address of the node to connect to in the form "host:port"
func WithAddress(address string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.address = address
	}
}

// WithIntersectPoints specifies the point(s) to use when starting the ChainSync operation. The default is to start at the genesis of the blockchain
func WithIntersectPoints(points []ocommon.Point) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.intersectPoints = points
	}
}

// WithInterceptTip specifies whether to start the ChainSync operation from the chain tip. The default is to start at the genesis of the blockchain
func WithIntersectTip(intersectTip bool) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.intersectTip = intersectTip
	}
}
