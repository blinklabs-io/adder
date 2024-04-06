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

package chainsync

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/blinklabs-io/snek/event"
	"github.com/blinklabs-io/snek/plugin"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/protocol/blockfetch"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ochainsync "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

const (
	// Size of cache for recent chainsync cursors
	cursorCacheSize = 20
)

type ChainSync struct {
	oConn            *ouroboros.Connection
	logger           plugin.Logger
	network          string
	networkMagic     uint32
	address          string
	socketPath       string
	ntcTcp           bool
	bulkMode         bool
	intersectTip     bool
	intersectPoints  []ocommon.Point
	includeCbor      bool
	autoReconnect    bool
	statusUpdateFunc StatusUpdateFunc
	status           *ChainSyncStatus
	errorChan        chan error
	eventChan        chan event.Event
	bulkRangeStart   ocommon.Point
	bulkRangeEnd     ocommon.Point
	cursorCache      []ocommon.Point
	dialAddress      string
	dialFamily       string
}

type ChainSyncStatus struct {
	SlotNumber    uint64
	BlockNumber   uint64
	BlockHash     string
	TipSlotNumber uint64
	TipBlockHash  string
	TipReached    bool
}

type StatusUpdateFunc func(ChainSyncStatus)

// New returns a new ChainSync object with the specified options applied
func New(options ...ChainSyncOptionFunc) *ChainSync {
	c := &ChainSync{
		errorChan:       make(chan error),
		eventChan:       make(chan event.Event, 10),
		intersectPoints: []ocommon.Point{},
		status:          &ChainSyncStatus{},
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
	// Start chainsync client
	c.oConn.ChainSync().Client.Start()
	if c.oConn.BlockFetch() != nil {
		c.oConn.BlockFetch().Client.Start()
	}
	if c.bulkMode && !c.intersectTip && c.oConn.BlockFetch() != nil {
		// Get available block range between our intersect point(s) and the chain tip
		var err error
		c.bulkRangeStart, c.bulkRangeEnd, err = c.oConn.ChainSync().Client.GetAvailableBlockRange(
			c.intersectPoints,
		)
		if err != nil {
			return err
		}
		if c.bulkRangeStart.Slot == 0 || c.bulkRangeEnd.Slot == 0 {
			// We're already at chain tip, so start a normal sync
			if err := c.oConn.ChainSync().Client.Sync(c.intersectPoints); err != nil {
				return err
			}
		} else {
			// Use BlockFetch to request the entire available block range at once
			if err := c.oConn.BlockFetch().Client.GetBlockRange(c.bulkRangeStart, c.bulkRangeEnd); err != nil {
				return err
			}
		}
	} else {
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
	// Lookup network by name, if provided
	if c.network != "" {
		network := ouroboros.NetworkByName(c.network)
		if network == ouroboros.NetworkInvalid {
			return fmt.Errorf("unknown network: %s", c.network)
		}
		c.networkMagic = network.NetworkMagic
		// If network has well-known public root address/port, use those as our dial default
		if network.PublicRootAddress != "" && network.PublicRootPort > 0 {
			c.dialFamily = "tcp"
			c.dialAddress = fmt.Sprintf(
				"%s:%d",
				network.PublicRootAddress,
				network.PublicRootPort,
			)
			useNtn = true
		}
	}
	// Use user-provided address or socket path, if provided
	if c.address != "" {
		c.dialFamily = "tcp"
		c.dialAddress = c.address
		if c.ntcTcp {
			useNtn = false
		} else {
			useNtn = true
		}
	} else if c.socketPath != "" {
		c.dialFamily = "unix"
		c.dialAddress = c.socketPath
		useNtn = false
	} else if c.dialFamily == "" || c.dialAddress == "" {
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
		ouroboros.WithBlockFetchConfig(
			blockfetch.NewConfig(
				blockfetch.WithBlockFunc(c.handleBlockFetchBlock),
			),
		),
	)
	if err != nil {
		return err
	}
	if err := c.oConn.Dial(c.dialFamily, c.dialAddress); err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.Infof("connected to node at %s", c.dialAddress)
	}
	// Start async error handler
	go func() {
		err, ok := <-c.oConn.ErrorChan()
		if ok {
			if c.autoReconnect {
				if c.logger != nil {
					c.logger.Infof("reconnecting to %s due to error: %s", c.dialAddress, err)
				}
				for {
					// Shutdown current connection
					if err := c.oConn.Close(); err != nil {
						if c.logger != nil {
							c.logger.Warnf("failed to properly close connection: %s", err)
						}
					}
					// Set the intersect points from the cursor cache
					if len(c.cursorCache) > 0 {
						c.intersectPoints = c.cursorCache[:]
					}
					// Restart the connection
					if err := c.Start(); err != nil {
						if c.logger != nil {
							c.logger.Infof("reconnecting to %s due to error: %s", c.dialAddress, err)
						}
						continue
					}
					break
				}
			} else {
				// Pass error through our own error channel
				c.errorChan <- err
			}
		}
	}()
	return nil
}

func (c *ChainSync) handleRollBackward(
	ctx chainsync.CallbackContext,
	point ocommon.Point,
	tip ochainsync.Tip,
) error {
	evt := event.New(
		"chainsync.rollback",
		time.Now(),
		nil,
		NewRollbackEvent(point),
	)
	c.eventChan <- evt
	return nil
}

func (c *ChainSync) handleRollForward(
	ctx chainsync.CallbackContext,
	blockType uint,
	blockData interface{},
	tip ochainsync.Tip,
) error {
	switch v := blockData.(type) {
	case ledger.Block:
		evt := event.New("chainsync.block", time.Now(), NewBlockContext(v, c.networkMagic), NewBlockEvent(v, c.includeCbor))
		c.eventChan <- evt
		c.updateStatus(v.SlotNumber(), v.BlockNumber(), v.Hash(), tip.Point.Slot, hex.EncodeToString(tip.Point.Hash))
	case ledger.BlockHeader:
		blockSlot := v.SlotNumber()
		blockHash, _ := hex.DecodeString(v.Hash())
		block, err := c.oConn.BlockFetch().Client.GetBlock(ocommon.Point{Slot: blockSlot, Hash: blockHash})
		if err != nil {
			return err
		}
		blockEvt := event.New("chainsync.block", time.Now(), NewBlockHeaderContext(v), NewBlockEvent(block, c.includeCbor))
		c.eventChan <- blockEvt
		for t, transaction := range block.Transactions() {
			txEvt := event.New("chainsync.transaction", time.Now(), NewTransactionContext(block, transaction, uint32(t), c.networkMagic), NewTransactionEvent(block, transaction, c.includeCbor))
			c.eventChan <- txEvt
		}
		c.updateStatus(v.SlotNumber(), v.BlockNumber(), v.Hash(), tip.Point.Slot, hex.EncodeToString(tip.Point.Hash))
	}
	return nil
}

func (c *ChainSync) handleBlockFetchBlock(ctx blockfetch.CallbackContext, block ledger.Block) error {
	blockEvt := event.New(
		"chainsync.block",
		time.Now(),
		NewBlockContext(block, c.networkMagic),
		NewBlockEvent(block, c.includeCbor),
	)
	c.eventChan <- blockEvt
	for t, transaction := range block.Transactions() {
		txEvt := event.New(
			"chainsync.transaction",
			time.Now(),
			NewTransactionContext(
				block,
				transaction,
				uint32(t),
				c.networkMagic,
			),
			NewTransactionEvent(block, transaction, c.includeCbor),
		)
		c.eventChan <- txEvt
	}
	c.updateStatus(
		block.SlotNumber(),
		block.BlockNumber(),
		block.Hash(),
		c.bulkRangeEnd.Slot,
		hex.EncodeToString(c.bulkRangeEnd.Hash),
	)
	// Start normal chain-sync if we've reached the last block of our bulk range
	if block.SlotNumber() == c.bulkRangeEnd.Slot {
		if err := c.oConn.ChainSync().Client.Sync([]ocommon.Point{c.bulkRangeEnd}); err != nil {
			return err
		}
	}
	return nil
}

func (c *ChainSync) updateStatus(
	slotNumber uint64,
	blockNumber uint64,
	blockHash string,
	tipSlotNumber uint64,
	tipBlockHash string,
) {
	// Update cursor cache
	blockHashBytes, _ := hex.DecodeString(blockHash)
	c.cursorCache = append(c.cursorCache, ocommon.Point{Slot: slotNumber, Hash: blockHashBytes})
	if len(c.cursorCache) > cursorCacheSize {
		c.cursorCache = c.cursorCache[len(c.cursorCache)-cursorCacheSize:]
	}
	// Determine if we've reached the chain tip
	if !c.status.TipReached {
		// Make sure we're past the end slot in any bulk range, since we don't update the tip during bulk sync
		if slotNumber > c.bulkRangeEnd.Slot {
			// Make sure our current slot is equal/higher than our last known tip slot
			if c.status.SlotNumber > 0 && slotNumber >= c.status.TipSlotNumber {
				c.status.TipReached = true
			}
		}
	}
	c.status.SlotNumber = slotNumber
	c.status.BlockNumber = blockNumber
	c.status.BlockHash = blockHash
	c.status.TipSlotNumber = tipSlotNumber
	c.status.TipBlockHash = tipBlockHash
	if c.statusUpdateFunc != nil {
		c.statusUpdateFunc(*(c.status))
	}
}
