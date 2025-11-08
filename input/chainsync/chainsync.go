// Copyright 2025 Blink Labs Software
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
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/ledger"
	blockfetch "github.com/blinklabs-io/gouroboros/protocol/blockfetch"
	ochainsync "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

// EpochFromSlot derives an epoch from a slot using Byron/Shelley genesis params.
// Byron slots: 0..EndSlot inclusive. Explicit zero EndSlot/ShelleyTransEpoch means no Byron era.
// Zero epoch length in either era yields a safe fallback (0 Byron / starting Shelley epoch).
func EpochFromSlot(slot uint64) uint64 {
	cfg := config.GetConfig()
	byron := cfg.ByronGenesis
	shelley := cfg.ShelleyGenesis

	endSlot := func() uint64 {
		if byron.EndSlot != nil {
			return *byron.EndSlot
		}
		return 0
	}()
	shelleyTransEpoch := func() uint64 {
		if cfg.ShelleyTransEpoch >= 0 {
			//nolint:gosec // ShelleyTransEpoch is controlled config, safe conversion
			return uint64(cfg.ShelleyTransEpoch)
		}
		return 0
	}()
	if slot <= endSlot {
		if byron.EpochLength == 0 {
			return 0 // avoid div by zero
		}
		return slot / byron.EpochLength
	}
	shelleyStartEpoch := shelleyTransEpoch
	shelleyStartSlot := endSlot + 1
	if shelley.EpochLength == 0 {
		return shelleyStartEpoch // avoid div by zero
	}
	return shelleyStartEpoch + (slot-shelleyStartSlot)/shelley.EpochLength
}

const (
	// Size of cache for recent chainsync cursors
	cursorCacheSize = 20

	blockBatchSize = 500

	maxAutoReconnectDelay = 60 * time.Second
	defaultKupoTimeout    = 30 * time.Second
)

type ChainSync struct {
	logger             plugin.Logger
	statusUpdateFunc   StatusUpdateFunc
	blockfetchDoneChan chan struct{}
	kupoClient         *kugo.Client
	oConn              *ouroboros.Connection
	eventChan          chan event.Event
	errorChan          chan error
	status             *ChainSyncStatus
	dialFamily         string
	kupoUrl            string
	network            string
	socketPath         string
	dialAddress        string
	address            string
	intersectPoints    []ocommon.Point
	pendingBlockPoints []ocommon.Point
	delayBuffer        [][]event.Event
	cursorCache        []ocommon.Point
	lastTip            ochainsync.Tip
	delayConfirmations uint
	autoReconnectDelay time.Duration
	networkMagic       uint32
	includeCbor        bool
	ntcTcp             bool
	intersectTip       bool
	autoReconnect      bool
}

type ChainSyncStatus struct {
	BlockHash     string
	TipBlockHash  string
	SlotNumber    uint64
	BlockNumber   uint64
	EpochNumber   uint64
	TipSlotNumber uint64
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
	// Use Kupo URL from global config
	c.kupoUrl = config.GetConfig().KupoUrl
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
	c.pendingBlockPoints = make([]ocommon.Point, 0)
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
		return errors.New("you must specify a host/port, UNIX socket path, or well-known network name")
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
				// Enable pipelining of RequestNext messages to speed up chainsync
				ochainsync.WithPipelineLimit(50),
			),
		),
		ouroboros.WithBlockFetchConfig(
			blockfetch.NewConfig(
				blockfetch.WithBlockFunc(c.handleBlockFetchBlock),
				blockfetch.WithBatchDoneFunc(c.handleBlockFetchBatchDone),
				// Set the recv queue size to 2x our block batch size
				blockfetch.WithRecvQueueSize(1000),
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
		c.logger.Info("connected to node at " + c.dialAddress)
	}
	// Start async error handler
	go func() {
		err, ok := <-c.oConn.ErrorChan()
		if ok {
			if c.autoReconnect {
				c.autoReconnectDelay = 0
				if c.logger != nil {
					c.logger.Error(fmt.Sprintf(
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
							c.logger.Error(fmt.Sprintf(
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
	c.lastTip = tip
	evt := event.New(
		"chainsync.rollback",
		time.Now(),
		nil,
		event.NewRollbackEvent(point),
	)
	// Remove rolled-back events from buffer
	if len(c.delayBuffer) > 0 {
		// We iterate backwards to avoid the issues with deleting from a list while iterating over it
		for i := len(c.delayBuffer) - 1; i >= 0; i-- {
			for _, evt := range c.delayBuffer[i] {
				// Look for block event
				if blockEvtCtx, ok := evt.Context.(event.BlockContext); ok {
					// Delete event batch if slot is after rollback point
					if blockEvtCtx.SlotNumber > point.Slot {
						c.delayBuffer = slices.Delete(c.delayBuffer, i, i+1)
						break
					}
				}
			}
		}
	}
	c.eventChan <- evt

	// updating status after roll backward
	c.updateStatus(
		point.Slot,                         // SlotNumber
		0,                                  // BlockNumber (unknown after rollback)
		hex.EncodeToString(point.Hash),     // BlockHash
		tip.Point.Slot,                     // TipSlotNumber
		hex.EncodeToString(tip.Point.Hash), // TipBlockHash
	)
	return nil
}

func (c *ChainSync) handleRollForward(
	ctx ochainsync.CallbackContext,
	blockType uint,
	blockData any,
	tip ochainsync.Tip,
) error {
	c.lastTip = tip
	var block ledger.Block
	tmpEvents := make([]event.Event, 0, 20)
	switch v := blockData.(type) {
	case ledger.Block:
		block = v
	case ledger.BlockHeader:
		c.pendingBlockPoints = append(
			c.pendingBlockPoints,
			ocommon.Point{
				Hash: v.Hash().Bytes(),
				Slot: v.SlotNumber(),
			},
		)
		// Don't fetch block unless we hit the batch size or are close to tip
		if v.SlotNumber() < (tip.Point.Slot-10000) && len(c.pendingBlockPoints) < blockBatchSize {
			return nil
		}
		// Request pending block range
		c.blockfetchDoneChan = make(chan struct{})
		if err := c.oConn.BlockFetch().Client.GetBlockRange(c.pendingBlockPoints[0], c.pendingBlockPoints[len(c.pendingBlockPoints)-1]); err != nil {
			return err
		}
		c.pendingBlockPoints = make([]ocommon.Point, 0)
		// Wait for block-fetch to finish
		<-c.blockfetchDoneChan
		return nil
	default:
		return errors.New("unknown type")
	}
	blockEvt := event.New(
		"chainsync.block",
		time.Now(),
		event.NewBlockHeaderContext(block.Header()),
		event.NewBlockEvent(block, c.includeCbor),
	)
	tmpEvents = append(tmpEvents, blockEvt)
	for t, transaction := range block.Transactions() {
		resolvedInputs, err := resolveTransactionInputs(transaction, c)
		if err != nil {
			return err
		}
		if t < 0 || t > math.MaxUint32 {
			return errors.New("invalid number of transactions")
		}
		txEvt := event.New(
			"chainsync.transaction",
			time.Now(),
			event.NewTransactionContext(
				block,
				transaction,
				uint32(t),
				c.networkMagic,
			),
			event.NewTransactionEvent(
				block,
				transaction,
				c.includeCbor,
				resolvedInputs,
			),
		)
		tmpEvents = append(tmpEvents, txEvt)
	}
	updateTip := ochainsync.Tip{
		Point: ocommon.Point{
			Slot: block.SlotNumber(),
			Hash: block.Hash().Bytes(),
		},
		BlockNumber: block.BlockNumber(),
	}
	if c.delayConfirmations == 0 {
		// Send events immediately if no delay confirmations configured
		for _, evt := range tmpEvents {
			c.eventChan <- evt
		}
	} else {
		// Add events to delay buffer
		c.delayBuffer = append(c.delayBuffer, tmpEvents)
		// Send oldest events and remove from buffer if delay buffer is larger than configured delay confirmations
		if uint(len(c.delayBuffer)) > c.delayConfirmations {
			for _, evt := range c.delayBuffer[0] {
				// Look for block event
				if blockEvt, ok := evt.Payload.(event.BlockEvent); ok {
					// Populate current point for update status based on most recently sent events
					updateTip = ochainsync.Tip{
						Point: ocommon.Point{
							Slot: blockEvt.Block.SlotNumber(),
							Hash: blockEvt.Block.Hash().Bytes(),
						},
						BlockNumber: blockEvt.Block.BlockNumber(),
					}
				}
				c.eventChan <- evt
			}
			c.delayBuffer = slices.Delete(c.delayBuffer, 0, 1)
		}
	}
	c.updateStatus(
		updateTip.Point.Slot,
		updateTip.BlockNumber,
		hex.EncodeToString(updateTip.Point.Hash),
		tip.Point.Slot,
		hex.EncodeToString(tip.Point.Hash),
	)
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
		event.NewBlockContext(block, c.networkMagic),
		event.NewBlockEvent(block, c.includeCbor),
	)
	c.eventChan <- blockEvt
	for t, transaction := range block.Transactions() {
		resolvedInputs, err := resolveTransactionInputs(transaction, c)
		if err != nil {
			return err
		}
		if t < 0 || t > math.MaxUint32 {
			return errors.New("invalid number of transactions")
		}
		txEvt := event.New(
			"chainsync.transaction",
			time.Now(),
			event.NewTransactionContext(
				block,
				transaction,
				uint32(t),
				c.networkMagic,
			),
			event.NewTransactionEvent(
				block,
				transaction,
				c.includeCbor,
				resolvedInputs,
			),
		)
		c.eventChan <- txEvt
	}
	c.updateStatus(
		block.SlotNumber(),
		block.BlockNumber(),
		block.Hash().String(),
		c.lastTip.Point.Slot,
		hex.EncodeToString(c.lastTip.Point.Hash),
	)
	return nil
}

func (c *ChainSync) handleBlockFetchBatchDone(
	ctx blockfetch.CallbackContext,
) error {
	close(c.blockfetchDoneChan)
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
		// Make sure our current slot is equal/higher than our last known tip slot
		if c.status.SlotNumber > 0 && slotNumber >= c.status.TipSlotNumber {
			c.status.TipReached = true
		}
	}
	c.status.SlotNumber = slotNumber
	c.status.BlockNumber = blockNumber
	c.status.BlockHash = blockHash
	c.status.EpochNumber = EpochFromSlot(slotNumber)
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

	// Validate URL first
	_, err := url.ParseRequestURI(c.kupoUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid kupo URL: %w", err)
	}

	KugoCustomLogger := logging.NewKugoCustomLogger(logging.LevelInfo)

	// Create client with timeout
	k := kugo.New(
		kugo.WithEndpoint(c.kupoUrl),
		kugo.WithLogger(KugoCustomLogger),
		kugo.WithTimeout(defaultKupoTimeout),
	)

	httpClient := &http.Client{
		Timeout: 2 * time.Second,
	}

	healthUrl := strings.TrimRight(c.kupoUrl, "/") + "/health"

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// Handle different error types
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, errors.New(
				"kupo health check timed out after 3 seconds",
			)
		case strings.Contains(err.Error(), "no such host"):
			return nil, fmt.Errorf("failed to resolve kupo host: %w", err)
		default:
			return nil, fmt.Errorf("failed to perform health check: %w", err)
		}
	}
	if resp == nil {
		return nil, errors.New("health check failed with nil response")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"health check failed with status code: %d",
			resp.StatusCode,
		)
	}

	c.kupoClient = k
	return k, nil
}

// resolveTransactionInputs resolves the transaction inputs by using the
// Kupo client and fetching the corresponding transaction outputs.
func resolveTransactionInputs(
	transaction ledger.Transaction,
	c *ChainSync,
) ([]ledger.TransactionOutput, error) {
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

			// Add timeout for matches query
			ctx, cancel := context.WithTimeout(
				context.Background(),
				defaultKupoTimeout,
			)
			defer cancel()

			// Create a simple transaction identifier
			txID := fmt.Sprintf("%d@%s", txIndex, txId)
			matches, err := k.Matches(ctx, kugo.Transaction(txID))
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return nil, fmt.Errorf(
						"kupo matches query timed out after %v",
						defaultKupoTimeout,
					)
				}
				return nil, fmt.Errorf(
					"error fetching matches for input TxId: %s, Index: %d. Error: %w",
					txId,
					txIndex,
					err,
				)
			}

			if len(matches) == 0 {
				slog.Warn(
					"no matches found for input, could be due to Kupo not in sync.",
					"txId",
					txId,
					"txIndex",
					txIndex,
				)
			} else {
				slog.Debug(fmt.Sprintf("found matches %d for input TxId: %s, Index: %d", len(matches), txId, txIndex))
				for _, match := range matches {
					slog.Debug(fmt.Sprintf("Match: %#v", match))
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
