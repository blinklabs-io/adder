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
	"sync"
	"time"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/nodeconn"
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

	// blockBatchSize controls how many blocks are requested per GetBlockRange
	// call during NtN catch-up sync. Must be small enough that the events
	// generated (~20 per block) fit in the eventChan buffer to avoid
	// backpressure that stalls the blockfetch recv queue and triggers
	// "message queue limit exceeded" errors.
	blockBatchSize = 50

	// eventChanBuffer must be large enough to absorb bursts during
	// catch-up sync. With PipelineLimit=50 and ~20 events per block we
	// can see 1000+ events queued before the output drains them.
	eventChanBuffer = 2048

	maxAutoReconnectDelay = 60 * time.Second
	defaultKupoTimeout    = 30 * time.Second
)

type ChainSync struct {
	plugin.Base
	statusUpdateFunc   StatusUpdateFunc
	blockfetchDoneChan chan struct{}
	kupoClient         *kugo.Client
	// connMu guards oConn. The connection is installed by setupConnection,
	// read by the chainsync callbacks and the supervisor, and taken by
	// Stop, which do not all run on the same goroutine. Reach it only
	// through conn, setConn, takeConn and closeConn.
	connMu             sync.Mutex
	oConn              *ouroboros.Connection
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
	networkMagic       uint32
	includeCbor        bool
	ntcTcp             bool
	intersectTip       bool
	autoReconnect      bool
	reconnectCallback  func()
}

// chainSyncBaseConfig is the Base configuration for this plugin. It is a
// function rather than a var so tests can call it to set up a ChainSync
// without going through Start, which needs a live node connection.
func chainSyncBaseConfig() plugin.BaseConfig {
	return plugin.BaseConfig{
		HasOutput:    true,
		OutputBuffer: eventChanBuffer,
	}
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
		intersectPoints: []ocommon.Point{},
		status:          &ChainSyncStatus{},
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// Role identifies this plugin as a pipeline input.
func (c *ChainSync) Role() plugin.PluginType { return plugin.PluginTypeInput }

// Start the chain sync input.
//
// The first connection is dialled synchronously, so a node that is down
// is reported to the caller rather than retried behind its back. Every
// later reconnect belongs to the supervisor.
//
// Reconnects use connect within the existing run and preserve its channels.
func (c *ChainSync) Start() error {
	return c.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (c *ChainSync) StartContext(ctx context.Context) error {
	return c.StartRun(ctx, chainSyncBaseConfig(), c.start, c.shutdownHooks())
}

func (c *ChainSync) start(ctx context.Context) error {
	connErrChan, err := c.connect(ctx)
	if err != nil {
		return err
	}
	c.Go(func() { c.superviseConnection(ctx, connErrChan) })
	return nil
}

// Stop the chain sync input.
//
// The connection is closed twice over, because two different orderings
// can leave one open:
//
//   - BeforeWait catches the settled case, and has to run there: closing
//     the connection is what unblocks the supervisor's read of the
//     connection error channel, so the wait would not end without it.
//   - AfterWait catches a dial that was in flight when Stop ran. There,
//     BeforeWait finds no connection to take, and the dial installs one
//     afterwards. By AfterWait the supervisor has exited and nothing can
//     call setConn again, so whatever is installed then is the last word.
//
// Without the second close, Stop returns nil while the node connection
// and its goroutines are still live, and those goroutines call back into
// a plugin whose channels are closing.
func (c *ChainSync) Stop() error {
	return c.Shutdown(c.shutdownHooks())
}

func (c *ChainSync) shutdownHooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{
		BeforeWait: func() error {
			return c.closeConn()
		},
		AfterWait: func() error {
			return c.closeConn()
		},
	}
}

// conn returns the current node connection, or nil when there is none:
// before the first dial, between a failure and its retry, or after Stop.
func (c *ChainSync) conn() *ouroboros.Connection {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.oConn
}

// setConn installs conn as the current node connection.
func (c *ChainSync) setConn(conn *ouroboros.Connection) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.oConn = conn
}

// takeConn clears the current node connection and returns it, so that
// exactly one of the racing callers gets a non-nil connection to close.
func (c *ChainSync) takeConn() *ouroboros.Connection {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	conn := c.oConn
	c.oConn = nil
	return conn
}

// closeConn closes the current node connection, if there is one. The
// close runs outside the lock: it blocks until the connection's own
// goroutines are done, and those call back into this plugin.
func (c *ChainSync) closeConn() error {
	conn := c.takeConn()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (c *ChainSync) setupConnection(ctx context.Context) error {
	// Determine connection parameters
	var useNtn bool
	// Lookup network by name, if provided
	if c.network != "" {
		network, ok := ouroboros.NetworkByName(c.network)
		if !ok {
			return fmt.Errorf("unknown network: %s", c.network)
		}
		if c.networkMagic == 0 {
			c.networkMagic = network.NetworkMagic
		}
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
	blockFetchConfig, err := blockfetch.NewConfig(
		blockfetch.WithBlockFunc(c.handleBlockFetchBlock),
		blockfetch.WithBatchDoneFunc(c.handleBlockFetchBatchDone),
		// Set the recv queue size to larger than our block batch size
		blockfetch.WithRecvQueueSize(512),
	)
	if err != nil {
		return err
	}
	// Create connection
	conn, err := nodeconn.Dial(ctx, c.dialFamily, c.dialAddress,
		ouroboros.WithNetworkMagic(c.networkMagic),
		ouroboros.WithNodeToNode(useNtn),
		ouroboros.WithKeepAlive(true),
		ouroboros.WithChainSyncConfig(
			ochainsync.NewConfig(
				ochainsync.WithRollForwardFunc(c.handleRollForward),
				ochainsync.WithRollBackwardFunc(c.handleRollBackward),
				// Enable pipelining of RequestNext messages to speed up chainsync
				ochainsync.WithPipelineLimit(50),
				// Recv queue must exceed pipeline limit to avoid "message queue
				// limit exceeded" errors during rapid catch-up sync
				ochainsync.WithRecvQueueSize(100),
			),
		),
		ouroboros.WithBlockFetchConfig(blockFetchConfig),
	)
	if err != nil {
		return err
	}
	if logger := c.Logger(); logger != nil {
		logger.Info("connected to node at " + c.dialAddress)
	}
	c.setConn(conn)
	return nil
}

// connect keeps a reconnect inside the current run without replacing channels.
func (c *ChainSync) connect(ctx context.Context) (<-chan error, error) {
	if err := c.setupConnection(ctx); err != nil {
		return nil, err
	}
	conn := c.conn()
	if conn == nil {
		// Stop took the connection while we were dialling.
		return nil, errors.New("chainsync: stopped while connecting")
	}
	// Protocol startup can block after the handshake. Close this attempt
	// on cancellation even before Stop can acquire the lifecycle lock.
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	// Start chainsync client
	conn.ChainSync().Client.Start()
	if conn.BlockFetch() != nil {
		conn.BlockFetch().Client.Start()
	}
	c.pendingBlockPoints = make([]ocommon.Point, 0)
	if c.intersectTip {
		tip, err := conn.ChainSync().Client.GetCurrentTip()
		if err != nil {
			return nil, err
		}
		c.intersectPoints = []ocommon.Point{tip.Point}
	}
	if err := conn.ChainSync().Client.Sync(c.intersectPoints); err != nil {
		return nil, err
	}
	return conn.ErrorChan(), nil
}

// superviseConnection owns the node connection for the life of a run. It
// waits for the current connection to fail and then either forwards the
// error or reconnects, until Stop signals.
//
// Reconnect stays in this tracked worker so Shutdown joins every connection
// attempt before closing channels. Calling Start here would contend with the
// lifecycle lock while Shutdown waits for this worker.
func (c *ChainSync) superviseConnection(
	ctx context.Context,
	connErrChan <-chan error,
) {
	done := c.Done()
	for {
		var err error
		var ok bool
		select {
		case <-done:
			return
		case err, ok = <-connErrChan:
			// A closed error stream is still a lost source; reconnect or
			// fail the run instead of leaving a healthy but idle pipeline.
			if !ok {
				err = errors.New("chainsync: connection closed")
			}
		}
		if ctx.Err() != nil {
			return
		}
		if !c.autoReconnect {
			// Pass the error through our own error channel, but check for
			// shutdown first.
			select {
			case <-done:
				return
			default:
			}
			c.Fail(err)
			return
		}
		if logger := c.Logger(); logger != nil {
			logger.Error(
				"reconnecting due to error",
				"address",
				c.dialAddress,
				"error",
				err,
			)
		}
		next, reconnected := c.reconnect(ctx, done)
		if !reconnected {
			return
		}
		connErrChan = next
		if c.reconnectCallback != nil {
			c.reconnectCallback()
		}
	}
}

// reconnect retries the node connection with the pre-existing exponential
// backoff until it succeeds, and reports the new connection's error
// channel. It gives up and reports false once done is closed, so a
// reconnect in flight does not outlive Stop.
//
// The backoff waits on done rather than sleeping, which is what lets Stop
// interrupt a wait that can reach maxAutoReconnectDelay.
func (c *ChainSync) reconnect(
	ctx context.Context,
	done <-chan struct{},
) (<-chan error, bool) {
	var delay time.Duration
	for {
		if delay > 0 {
			if logger := c.Logger(); logger != nil {
				logger.Info("waiting to reconnect", "delay", delay)
			}
			select {
			case <-done:
				return nil, false
			case <-time.After(delay):
			}
			// Double current reconnect delay up to maximum
			delay = min(delay*2, maxAutoReconnectDelay)
		} else {
			// Set initial reconnect delay
			delay = 1 * time.Second
		}
		// Retire the failed connection before dialling a replacement.
		if err := c.closeConn(); err != nil {
			if logger := c.Logger(); logger != nil {
				logger.Warn(
					"failed to properly close connection",
					"error",
					err,
				)
			}
		}
		select {
		case <-done:
			return nil, false
		default:
		}
		// Set the intersect points from the cursor cache
		if len(c.cursorCache) > 0 {
			c.intersectPoints = c.cursorCache[:]
		}
		connErrChan, err := c.connect(ctx)
		if err != nil {
			if logger := c.Logger(); logger != nil {
				logger.Error(
					"reconnecting due to error",
					"address",
					c.dialAddress,
					"error",
					err,
				)
			}
			continue
		}
		return connErrChan, true
	}
}

func (c *ChainSync) handleRollBackward(
	ctx ochainsync.CallbackContext,
	point ocommon.Point,
	tip ochainsync.Tip,
) error {
	c.lastTip = tip
	evt := event.New(
		event.TypeRollback,
		time.Now(),
		nil,
		event.NewRollbackEvent(point),
	)
	// Remove rolled-back events from buffer
	if len(c.delayBuffer) > 0 {
		// We iterate backwards to avoid the issues with deleting from a list while iterating over it
		// slices.Backward is deliberately not used here: it captures the
		// slice header once, so the values it yields come from the
		// pre-deletion view. That happens to be equivalent while the
		// deletions walk downward, but the equivalence rests on aliasing
		// the backing array rather than on anything the loop states.
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
	_ = c.Emit(evt)

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
		// This callback runs on a connection goroutine, so the connection
		// can be taken by Stop underneath it.
		conn := c.conn()
		if conn == nil {
			return errors.New("chainsync: connection closed during block fetch")
		}
		if err := conn.BlockFetch().Client.GetBlockRange(c.pendingBlockPoints[0], c.pendingBlockPoints[len(c.pendingBlockPoints)-1]); err != nil {
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
		event.TypeBlock,
		time.Now(),
		event.NewBlockHeaderContext(block.Header(), c.networkMagic),
		event.NewBlockEvent(block, c.includeCbor),
	)
	tmpEvents = append(tmpEvents, blockEvt)
	for t, transaction := range block.Transactions() {
		resolvedInputs, err := resolveTransactionInputs(transaction, c)
		if err != nil {
			slog.Error(
				"failed to resolve transaction inputs via Kupo, emitting without resolved inputs",
				"err",
				err,
			)
			resolvedInputs = nil
		}
		if t < 0 || t > math.MaxUint32 {
			return errors.New("invalid number of transactions")
		}
		txEvt := event.New(
			event.TypeTransaction,
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
		// Emit governance event if transaction contains governance data
		if event.HasGovernanceData(transaction) {
			govEvt := event.New(
				event.TypeGovernance,
				time.Now(),
				event.NewGovernanceContext(
					block,
					transaction,
					//nolint:gosec // t is bounds-checked above
					uint32(t),
					c.networkMagic,
				),
				event.NewGovernanceEvent(
					block,
					transaction,
					c.includeCbor,
				),
			)
			tmpEvents = append(tmpEvents, govEvt)
		}
		// Emit DRep certificate events
		if drepCerts := event.ExtractDRepCertificates(transaction); len(
			drepCerts,
		) > 0 {
			drepCtx := event.NewGovernanceContext(
				block,
				transaction,
				//nolint:gosec // t is bounds-checked above
				uint32(t),
				c.networkMagic,
			)
			for _, cert := range drepCerts {
				if evtType, ok := event.DRepEventType(cert.CertificateType); ok {
					drepEvt := event.New(
						evtType,
						time.Now(),
						drepCtx,
						event.NewDRepCertificateEvent(block, cert),
					)
					tmpEvents = append(tmpEvents, drepEvt)
				}
			}
		}
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
			_ = c.Emit(evt)
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
				_ = c.Emit(evt)
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
		event.TypeBlock,
		time.Now(),
		event.NewBlockContext(block, c.networkMagic),
		event.NewBlockEvent(block, c.includeCbor),
	)
	_ = c.Emit(blockEvt)
	for t, transaction := range block.Transactions() {
		resolvedInputs, err := resolveTransactionInputs(transaction, c)
		if err != nil {
			slog.Error(
				"failed to resolve transaction inputs via Kupo, emitting without resolved inputs",
				"err",
				err,
			)
			resolvedInputs = nil
		}
		if t < 0 || t > math.MaxUint32 {
			return errors.New("invalid number of transactions")
		}
		txEvt := event.New(
			event.TypeTransaction,
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
		_ = c.Emit(txEvt)
		// Emit governance event if transaction contains governance data
		if event.HasGovernanceData(transaction) {
			govEvt := event.New(
				event.TypeGovernance,
				time.Now(),
				event.NewGovernanceContext(
					block,
					transaction,
					//nolint:gosec // t is bounds-checked above
					uint32(t),
					c.networkMagic,
				),
				event.NewGovernanceEvent(
					block,
					transaction,
					c.includeCbor,
				),
			)
			_ = c.Emit(govEvt)
		}
		// Emit DRep certificate events
		if drepCerts := event.ExtractDRepCertificates(transaction); len(
			drepCerts,
		) > 0 {
			drepCtx := event.NewGovernanceContext(
				block,
				transaction,
				//nolint:gosec // t is bounds-checked above
				uint32(t),
				c.networkMagic,
			)
			for _, cert := range drepCerts {
				if evtType, ok := event.DRepEventType(cert.CertificateType); ok {
					drepEvt := event.New(
						evtType,
						time.Now(),
						drepCtx,
						event.NewDRepCertificateEvent(block, cert),
					)
					_ = c.Emit(drepEvt)
				}
			}
		}
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
		c.statusUpdateFunc(*c.status)
	}
}

func getKupoClient(c *ChainSync) (*kugo.Client, error) {
	if c.kupoClient != nil {
		return c.kupoClient, nil
	}

	// Validate URL first
	kupoURL, err := url.ParseRequestURI(c.kupoUrl)
	if err != nil {
		return nil, fmt.Errorf("invalid kupo URL: %w", err)
	}
	if kupoURL.Scheme != "http" && kupoURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid kupo URL scheme: %s", kupoURL.Scheme)
	}
	if kupoURL.Host == "" {
		return nil, errors.New("invalid kupo URL host")
	}

	KugoCustomLogger := logging.NewKugoCustomLoggerWithLogger(
		logging.GetLoggerForComponent("kupo"),
	)

	// Create client with timeout
	k := kugo.New(
		kugo.WithEndpoint(c.kupoUrl),
		kugo.WithLogger(KugoCustomLogger),
		kugo.WithTimeout(defaultKupoTimeout),
	)

	httpClient := &http.Client{
		Timeout: 2 * time.Second,
	}

	healthURL := kupoURL.JoinPath("health")

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		healthURL.String(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create health check request: %w", err)
	}

	// #nosec G704 -- Kupo endpoint is user-configured and validated before use.
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

			logger := logging.GetLoggerForComponent("input.chainsync")
			if len(matches) == 0 {
				logger.Warn(
					"no matches found for input, could be due to Kupo not in sync.",
					"txId",
					txId,
					"txIndex",
					txIndex,
				)
			} else {
				logger.Debug(
					"found matches for input",
					"count",
					len(matches),
					"txId",
					txId,
					"txIndex",
					txIndex,
				)
				for _, match := range matches {
					logger.Debug(
						"resolved match detail",
						"match",
						match,
					)
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

var _ plugin.ManagedPlugin = (*ChainSync)(nil)
