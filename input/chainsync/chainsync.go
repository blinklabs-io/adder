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
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SundaeSwap-finance/kugo"

	"github.com/SundaeSwap-finance/ogmigo/v6/ouroboros/chainsync"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/ledger"
	"github.com/blinklabs-io/gouroboros/protocol/blockfetch"
	ochainsync "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

const (
	// Size of cache for recent chainsync cursors
	cursorCacheSize = 20

	maxAutoReconnectDelay = 60 * time.Second
)

type ChainSync struct {
	oConn              *ouroboros.Connection
	logger             plugin.Logger
	network            string
	networkMagic       uint32
	address            string
	socketPath         string
	ntcTcp             bool
	bulkMode           bool
	intersectTip       bool
	intersectPoints    []ocommon.Point
	includeCbor        bool
	autoReconnect      bool
	autoReconnectDelay time.Duration
	statusUpdateFunc   StatusUpdateFunc
	status             *ChainSyncStatus
	errorChan          chan error
	eventChan          chan event.Event
	bulkRangeStart     ocommon.Point
	bulkRangeEnd       ocommon.Point
	cursorCache        []ocommon.Point
	dialAddress        string
	dialFamily         string
	kupoUrl            string
	kupoClient         *kugo.Client
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
		network, ok := ouroboros.NetworkByName(c.network)
		if !ok {
			return fmt.Errorf("unknown network: %s", c.network)
		}
		c.networkMagic = network.NetworkMagic
		// If network has well-known public root address/port, use those as our dial default
		if len(network.BootstrapPeers) > 0 {
			peer := network.BootstrapPeers[0]
			c.dialFamily = "tcp"
			c.dialAddress = fmt.Sprintf(
				"%s:%d",
				peer.Address,
				peer.Port,
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
		c.logger.Info(fmt.Sprintf("connected to node at %s", c.dialAddress))
	}
	// Start async error handler
	go func() {
		err, ok := <-c.oConn.ErrorChan()
		if ok {
			if c.autoReconnect {
				c.autoReconnectDelay = 0
				if c.logger != nil {
					c.logger.Info(fmt.Sprintf(
						"reconnecting to %s due to error: %s",
						c.dialAddress,
						err,
					))
				}
				for {
					if c.autoReconnectDelay > 0 {
						c.logger.Info(fmt.Sprintf(
							"waiting %s to reconnect",
							c.autoReconnectDelay,
						))
						time.Sleep(c.autoReconnectDelay)
						// Double current reconnect delay up to maximum
						c.autoReconnectDelay = min(
							c.autoReconnectDelay*2,
							maxAutoReconnectDelay,
						)
					} else {
						// Set initial reconnect delay
						c.autoReconnectDelay = 1 * time.Second
					}
					// Shutdown current connection
					if err := c.oConn.Close(); err != nil {
						if c.logger != nil {
							c.logger.Warn(fmt.Sprintf(
								"failed to properly close connection: %s",
								err,
							))
						}
					}
					// Set the intersect points from the cursor cache
					if len(c.cursorCache) > 0 {
						c.intersectPoints = c.cursorCache[:]
					}
					// Restart the connection
					if err := c.Start(); err != nil {
						if c.logger != nil {
							c.logger.Info(fmt.Sprintf(
								"reconnecting to %s due to error: %s",
								c.dialAddress,
								err,
							))
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
	ctx ochainsync.CallbackContext,
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
	ctx ochainsync.CallbackContext,
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
			resolvedInputs, err := resolveTransactionInputs(transaction, c)
			if err != nil {
				return err
			}
			txEvt := event.New("chainsync.transaction", time.Now(), NewTransactionContext(block, transaction, uint32(t), c.networkMagic),
				NewTransactionEvent(block, transaction, c.includeCbor, resolvedInputs))
			c.eventChan <- txEvt
		}
		c.updateStatus(v.SlotNumber(), v.BlockNumber(), v.Hash(), tip.Point.Slot, hex.EncodeToString(tip.Point.Hash))
	}
	return nil
}

func (c *ChainSync) handleBlockFetchBlock(
	ctx blockfetch.CallbackContext,
	blockType uint,
	block ledger.Block,
) error {
	blockEvt := event.New(
		"chainsync.block",
		time.Now(),
		NewBlockContext(block, c.networkMagic),
		NewBlockEvent(block, c.includeCbor),
	)
	c.eventChan <- blockEvt
	for t, transaction := range block.Transactions() {
		resolvedInputs, err := resolveTransactionInputs(transaction, c)
		if err != nil {
			return err
		}
		txEvt := event.New(
			"chainsync.transaction",
			time.Now(),
			NewTransactionContext(
				block,
				transaction,
				uint32(t),
				c.networkMagic,
			),
			NewTransactionEvent(block, transaction, c.includeCbor, resolvedInputs),
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
	c.cursorCache = append(
		c.cursorCache,
		ocommon.Point{Slot: slotNumber, Hash: blockHashBytes},
	)
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

func getKupoClient(c *ChainSync) (*kugo.Client, error) {
	if c.kupoClient != nil {
		return c.kupoClient, nil
	}

	KugoCustomLogger := logging.NewKugoCustomLogger(logging.LevelInfo)

	k := kugo.New(
		kugo.WithEndpoint(c.kupoUrl),
		kugo.WithLogger(KugoCustomLogger),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	healthUrl := strings.TrimRight(c.kupoUrl, "/") + "/v1/health"
	req, err := http.NewRequestWithContext(ctx, "GET", healthUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create health check request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform health check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check failed with status code: %d", resp.StatusCode)
	}

	c.kupoClient = k

	return k, nil
}

// resolveTransactionInputs resolves the transaction inputs by using the
// Kupo client and fetching the corresponding transaction outputs.
func resolveTransactionInputs(transaction ledger.Transaction, c *ChainSync) ([]ledger.TransactionOutput, error) {
	var resolvedInputs []ledger.TransactionOutput

	// Use Kupo client to resolve inputs if available
	if c.kupoUrl != "" {
		k, err := getKupoClient(c)
		if err != nil {
			return nil, fmt.Errorf("failed to get Kupo client: %w", err)
		}

		for _, input := range transaction.Inputs() {
			// Extract transaction ID and index from the input
			txId := input.Id().String()
			txIndex := int(input.Index())
			matches, err := k.Matches(context.Background(),
				kugo.TxOut(chainsync.NewTxID(txId, txIndex)),
			)
			if err != nil {
				return nil, fmt.Errorf("Error fetching matches for input TxId: %s, Index: %d. Error: %w\n", txId, txIndex, err)
			}

			if len(matches) == 0 {
				slog.Info("No matches found for input TxId: %s, Index: %d, could be due to Kupo not in sync\n", txId, txIndex)
			} else {
				slog.Debug(fmt.Sprintf("Found matches %d for input TxId: %s, Index: %d\n", len(matches), txId, txIndex))
				for _, match := range matches {
					slog.Debug(fmt.Sprintf("Match: %#v\n", match))
					transactionOutput, err := NewResolvedTransactionOutput(match)
					if err != nil {
						return nil, err
					}
					resolvedInputs = append(resolvedInputs, transactionOutput)
				}
			}
		}
	}
	return resolvedInputs, nil
}
