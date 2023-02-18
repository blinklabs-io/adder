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

// WithNodeToNode specifies whether to use the node-to-node protocol. The default is to use node-to-client
func WithNodeToNode(nodeToNode bool) ChainSyncOptionFunc {
	return func(o *ChainSync) {
		o.useNodeToNode = nodeToNode
	}
}

// WithSocketPath specifies the socket path of the node to connect to
func WithSocketPath(socketPath string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.socketPath = socketPath
	}
}

// WithAddress specifies the TCP address of the node to connect to
func WithAddress(address string) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.address = address
	}
}

// WithPort specifies the TCP port of the node to connect to
func WithPort(port uint) ChainSyncOptionFunc {
	return func(c *ChainSync) {
		c.port = port
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
