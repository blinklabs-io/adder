package chainsync

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/blinklabs-io/snek/event"

	"github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/ledger"
	ochainsync "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

type ChainSync struct {
	oConn              *ouroboros.Connection
	network            string
	networkMagic       uint32
	address            string
	socketPath         string
	ntcTcp             bool
	intersectTip       bool
	intersectPoints    []ocommon.Point
	errorChan          chan error
	eventChan          chan event.Event
	byronEpochBaseSlot uint64
	byronEpochSlot     uint64
}

// New returns a new ChainSync object with the specified options applied
func New(options ...ChainSyncOptionFunc) *ChainSync {
	c := &ChainSync{
		errorChan:       make(chan error),
		eventChan:       make(chan event.Event, 10),
		intersectPoints: []ocommon.Point{},
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Start the chain sync input
func (c *ChainSync) Start() error {
	if err := c.setupConnection(); err != nil {
		return err
	}
	c.oConn.ChainSync().Client.Start()
	if c.oConn.BlockFetch() != nil {
		c.oConn.BlockFetch().Client.Start()
	}
	if c.intersectTip {
		tip, err := c.oConn.ChainSync().Client.GetCurrentTip()
		if err != nil {
			return err
		}
		c.intersectPoints = []ocommon.Point{tip.Point}
	}
	if err := c.oConn.ChainSync().Client.Sync(c.intersectPoints); err != nil {
		return err
	}
	return nil
}

// Stop the chain sync input
func (c *ChainSync) Stop() error {
	err := c.oConn.Close()
	close(c.eventChan)
	close(c.errorChan)
	return err
}

// ErrorChan returns the input error channel
func (c *ChainSync) ErrorChan() chan error {
	return c.errorChan
}

// InputChan always returns nil
func (c *ChainSync) InputChan() chan<- event.Event {
	return nil
}

// OutputChan returns the output event channel
func (c *ChainSync) OutputChan() <-chan event.Event {
	return c.eventChan
}

func (c *ChainSync) setupConnection() error {
	// Determine connection parameters
	var useNtn bool
	var dialFamily, dialAddress string
	// Lookup network by name, if provided
	if c.network != "" {
		network := ouroboros.NetworkByName(c.network)
		if network == ouroboros.NetworkInvalid {
			return fmt.Errorf("unknown network: %s", c.network)
		}
		c.networkMagic = network.NetworkMagic
		// If network has well-known public root address/port, use those as our dial default
		if network.PublicRootAddress != "" && network.PublicRootPort > 0 {
			dialFamily = "tcp"
			dialAddress = fmt.Sprintf("%s:%d", network.PublicRootAddress, network.PublicRootPort)
			useNtn = true
		}
	}
	// Use user-provided address or socket path, if provided
	if c.address != "" {
		dialFamily = "tcp"
		dialAddress = c.address
		if c.ntcTcp {
			useNtn = false
		} else {
			useNtn = true
		}
	} else if c.socketPath != "" {
		dialFamily = "unix"
		dialAddress = c.socketPath
		useNtn = false
	} else if dialFamily == "" || dialAddress == "" {
		return fmt.Errorf("you must specify a host/port, UNIX socket path, or well-known network name")
	}
	// Create connection
	var err error
	c.oConn, err = ouroboros.NewConnection(
		ouroboros.WithNetworkMagic(c.networkMagic),
		ouroboros.WithNodeToNode(useNtn),
		ouroboros.WithKeepAlive(true),
		ouroboros.WithChainSyncConfig(
			ochainsync.NewConfig(
				ochainsync.WithRollForwardFunc(c.handleRollForward),
				ochainsync.WithRollBackwardFunc(c.handleRollBackward),
			),
		),
	)
	if err != nil {
		return err
	}
	if err := c.oConn.Dial(dialFamily, dialAddress); err != nil {
		return err
	}
	// Start async error handler
	go func() {
		err, ok := <-c.oConn.ErrorChan()
		if ok {
			// Pass error through our own error channel
			c.errorChan <- err
			return
		}
		close(c.errorChan)
	}()
	return nil
}

func (c *ChainSync) handleRollBackward(point ocommon.Point, tip ochainsync.Tip) error {
	evt := event.New("rollback", time.Now(), NewRollbackEvent(point))
	c.eventChan <- evt
	return nil
}

func (c *ChainSync) handleRollForward(blockType uint, blockData interface{}, tip ochainsync.Tip) error {
	switch v := blockData.(type) {
	case ledger.Block:
		evt := event.New("block", time.Now(), NewBlockEvent(v))
		c.eventChan <- evt
	case ledger.BlockHeader:
		var blockSlot uint64
		var blockHash []byte
		switch blockType {
		case ledger.BLOCK_TYPE_BYRON_EBB:
			h := blockData.(*ledger.ByronEpochBoundaryBlockHeader)
			if c.byronEpochSlot > 0 {
				c.byronEpochBaseSlot += c.byronEpochSlot + 1
			}
			blockSlot = c.byronEpochBaseSlot
			blockHash, _ = hex.DecodeString(h.Hash())
		case ledger.BLOCK_TYPE_BYRON_MAIN:
			h := blockData.(*ledger.ByronMainBlockHeader)
			c.byronEpochSlot = uint64(h.ConsensusData.SlotId.Slot)
			blockSlot = c.byronEpochBaseSlot + c.byronEpochSlot
			blockHash, _ = hex.DecodeString(h.Hash())
		default:
			blockSlot = v.SlotNumber()
			blockHash, _ = hex.DecodeString(v.Hash())
		}
		block, err := c.oConn.BlockFetch().Client.GetBlock(ocommon.Point{Slot: blockSlot, Hash: blockHash})
		if err != nil {
			return err
		}
		blockEvt := event.New("block", time.Now(), NewBlockEvent(block))
		c.eventChan <- blockEvt
		for _, transaction := range block.Transactions() {
			txEvt := event.New("transaction", time.Now(), NewTransactionEvent(block, transaction))
			c.eventChan <- txEvt
		}
	}
	return nil
}
