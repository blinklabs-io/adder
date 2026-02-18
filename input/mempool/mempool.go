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

package mempool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/input/chainsync"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/ledger"
	localtxmonitor "github.com/blinklabs-io/gouroboros/protocol/localtxmonitor"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultKupoTimeout  = 30 * time.Second
	kupoHealthTimeout   = 3 * time.Second
)

type Mempool struct {
	logger          plugin.Logger
	network         string
	networkMagic    uint32
	socketPath      string
	address         string
	ntcTcp          bool
	includeCbor     bool
	pollIntervalStr string
	pollInterval    time.Duration
	kupoUrl         string

	eventChan    chan event.Event
	errorChan    chan error
	doneChan     chan struct{}
	wg           sync.WaitGroup
	stopOnce     sync.Once // idempotent Stop (same pattern as pipeline.Pipeline)
	oConn        *ouroboros.Connection
	dialFamily   string
	dialAddress  string
	seenTxHashes map[string]struct{}

	kupoClient               *kugo.Client
	kupoDisabled             bool
	kupoInvalidPatternLogged bool
}

// New returns a new Mempool input plugin
func New(opts ...MempoolOptionFunc) *Mempool {
	m := &Mempool{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Start connects to the node and starts polling the mempool.
// Safe to call again to restart (e.g. when the pipeline is restarted via
// Stop() then Start()). Event and error channels are reused when non-nil so
// that the pipeline's goroutines reading from OutputChan()/ErrorChan() never
// see a closed channel; after Stop() they are nil so the next Start() creates
// new channels and the pipeline obtains fresh references.
func (m *Mempool) Start() error {
	m.stopOnce = sync.Once{} // reset so next Stop() runs (Pipeline resets on restart too)
	if m.doneChan != nil {
		close(m.doneChan)
		m.wg.Wait()
	}
	if m.oConn != nil {
		_ = m.oConn.Close()
		m.oConn = nil
	}
	if m.eventChan == nil {
		m.eventChan = make(chan event.Event, 10)
	}
	if m.errorChan == nil {
		m.errorChan = make(chan error, 1)
	}
	m.doneChan = make(chan struct{})

	if m.kupoUrl == "" {
		m.kupoUrl = config.GetConfig().KupoUrl
	}
	if m.kupoUrl == "" {
		m.logger.Info("Kupo URL not set; resolved inputs will be omitted (set KUPO_URL or --input-mempool-kupo-url)")
	} else {
		m.logger.Info("Using Kupo for input resolution", "url", m.kupoUrl)
	}

	if err := m.setupConnection(); err != nil {
		return err
	}

	m.oConn.LocalTxMonitor().Client.Start()

	m.wg.Add(1)
	go m.pollLoop()
	return nil
}

// Stop shuts down the connection and stops polling.
// Idempotent and safe to call multiple times, following the Pipeline's
// pattern (pipeline/pipeline.go): shutdown logic runs inside sync.Once so
// multiple Stop() calls never double-close channels.
func (m *Mempool) Stop() error {
	m.stopOnce.Do(func() {
		if m.doneChan != nil {
			close(m.doneChan)
			m.doneChan = nil
		}
		if m.oConn != nil {
			_ = m.oConn.Close()
			m.oConn = nil
		}
		m.wg.Wait()
		if m.eventChan != nil {
			close(m.eventChan)
			m.eventChan = nil
		}
		if m.errorChan != nil {
			close(m.errorChan)
			m.errorChan = nil
		}
	})
	return nil
}

// ErrorChan returns the plugin's error channel
func (m *Mempool) ErrorChan() <-chan error {
	return m.errorChan
}

// InputChan returns nil (mempool is an input-only plugin)
func (m *Mempool) InputChan() chan<- event.Event {
	return nil
}

// OutputChan returns the channel of mempool transaction events
func (m *Mempool) OutputChan() <-chan event.Event {
	return m.eventChan
}

func (m *Mempool) setupConnection() error {
	if m.network != "" {
		network, ok := ouroboros.NetworkByName(m.network)
		if !ok {
			return fmt.Errorf("unknown network: %s", m.network)
		}
		if m.networkMagic == 0 {
			m.networkMagic = network.NetworkMagic
		}
	}
	if m.address != "" {
		m.dialFamily = "tcp"
		m.dialAddress = m.address
		if !m.ntcTcp {
			return errors.New("address requires input-mempool-ntc-tcp=true for NtC over TCP")
		}
	} else if m.socketPath != "" {
		m.dialFamily = "unix"
		m.dialAddress = m.socketPath
	} else {
		return errors.New("must specify input-mempool-socket-path or input-mempool-address")
	}
	if m.networkMagic == 0 {
		return errors.New("must specify input-mempool-network or input-mempool-network-magic")
	}

	m.pollInterval = defaultPollInterval
	if m.pollIntervalStr != "" {
		d, err := time.ParseDuration(m.pollIntervalStr)
		if err != nil {
			return fmt.Errorf("invalid poll interval: %w", err)
		}
		if d <= 0 {
			return errors.New("poll interval must be positive")
		}
		m.pollInterval = d
	}

	cfg := localtxmonitor.NewConfig(
		localtxmonitor.WithAcquireTimeout(10*time.Second),
		localtxmonitor.WithQueryTimeout(30*time.Second),
	)
	oConn, err := ouroboros.NewConnection(
		ouroboros.WithNetworkMagic(m.networkMagic),
		ouroboros.WithNodeToNode(false),
		ouroboros.WithKeepAlive(true),
		ouroboros.WithLocalTxMonitorConfig(cfg),
	)
	if err != nil {
		return err
	}
	if err := oConn.Dial(m.dialFamily, m.dialAddress); err != nil {
		_ = oConn.Close()
		return err
	}
	m.oConn = oConn
	if m.logger != nil {
		m.logger.Info("connected to node for mempool", "address", m.dialAddress)
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			select {
			case <-m.doneChan:
				return
			case err, ok := <-m.oConn.ErrorChan():
				if !ok {
					return
				}
				select {
				case <-m.doneChan:
					return
				case m.errorChan <- err:
				}
			}
		}
	}()
	return nil
}

func (m *Mempool) pollLoop() {
	defer m.wg.Done()
	if m.pollInterval <= 0 {
		m.pollInterval = defaultPollInterval
	}
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.doneChan:
			return
		case <-ticker.C:
			m.pollOnce()
		}
	}
}

func (m *Mempool) pollOnce() {
	if m.oConn == nil {
		return
	}
	client := m.oConn.LocalTxMonitor().Client
	if client == nil {
		return
	}
	if err := client.Acquire(); err != nil {
		if m.logger != nil {
			m.logger.Warn("mempool acquire failed", "error", err)
		}
		return
	}
	defer func() {
		_ = client.Release()
	}()

	_, _, numTxs, err := client.GetSizes()
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("mempool GetSizes failed", "error", err)
		}
		return
	}
	if numTxs == 0 {
		return
	}
	if m.seenTxHashes == nil {
		m.seenTxHashes = make(map[string]struct{})
	}

	// Collect all txs this poll. We only need to remember last poll's hashes
	// to emit events only for newly seen transactions.
	type pollTx struct {
		hash string
		tx   ledger.Transaction
	}
	var pollTxs []pollTx
	for {
		select {
		case <-m.doneChan:
			return
		default:
		}
		txCbor, err := client.NextTx()
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("mempool NextTx failed", "error", err)
			}
			return
		}
		if len(txCbor) == 0 {
			break
		}
		tx, err := m.parseTx(txCbor)
		if err != nil {
			if m.logger != nil {
				m.logger.Debug("mempool skip tx parse error", "error", err, "cbor_len", len(txCbor))
			}
			continue
		}
		txHash := tx.Hash().String()
		pollTxs = append(pollTxs, pollTx{hash: txHash, tx: tx})
	}

	thisPollHashes := make(map[string]struct{}, len(pollTxs))
	for _, p := range pollTxs {
		thisPollHashes[p.hash] = struct{}{}
	}

	for _, p := range pollTxs {
		if _, seen := m.seenTxHashes[p.hash]; seen {
			continue
		}
		ctx := event.NewMempoolTransactionContext(p.tx, 0, m.networkMagic)
		payload := event.NewTransactionEventFromTx(p.tx, m.includeCbor)
		if m.kupoUrl != "" && !m.kupoDisabled {
			resolvedInputs, err := m.resolveTransactionInputs(p.tx)
			if err != nil {
				if m.logger != nil {
					m.logger.Warn("failed to resolve transaction inputs via Kupo, emitting without resolved inputs", "error", err)
				}
			} else if len(resolvedInputs) > 0 {
				payload.ResolvedInputs = resolvedInputs
			}
		}
		evt := event.New("input.transaction", time.Now(), ctx, payload)
		select {
		case <-m.doneChan:
			return
		case m.eventChan <- evt:
		}
	}

	// Remember only this poll's hashes for next time (no unbounded growth).
	m.seenTxHashes = thisPollHashes
}

func (m *Mempool) parseTx(data []byte) (ledger.Transaction, error) {
	txType, err := ledger.DetermineTransactionType(data)
	if err != nil {
		return nil, err
	}
	return ledger.NewTransactionFromCbor(txType, data)
}

func (m *Mempool) getKupoClient() (*kugo.Client, error) {
	if m.kupoClient != nil {
		return m.kupoClient, nil
	}
	urlStr := m.kupoUrl
	if urlStr == "" {
		return nil, errors.New("kupo URL not configured")
	}
	_, err := url.ParseRequestURI(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid kupo URL: %w", err)
	}
	kugoLogger := logging.NewKugoCustomLogger(logging.LevelInfo)
	k := kugo.New(
		kugo.WithEndpoint(urlStr),
		kugo.WithLogger(kugoLogger),
		kugo.WithTimeout(defaultKupoTimeout),
	)
	healthURL := strings.TrimRight(urlStr, "/") + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), kupoHealthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create health check request: %w", err)
	}
	httpClient := &http.Client{Timeout: kupoHealthTimeout}
	// #nosec G704 -- Kupo endpoint is user-configured and validated before use.
	resp, err := httpClient.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, errors.New("kupo health check timed out after 3 seconds")
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
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("health check failed with status code: %d", resp.StatusCode)
	}
	m.kupoClient = k
	return k, nil
}

func (m *Mempool) resolveTransactionInputs(tx ledger.Transaction) ([]ledger.TransactionOutput, error) {
	var resolvedInputs []ledger.TransactionOutput
	k, err := m.getKupoClient()
	if err != nil {
		return nil, err
	}
	for _, input := range tx.Inputs() {
		txID := input.Id().String()
		txIndex := int(input.Index())
		// Kupo output-reference pattern: output_index@transaction_id (see Kupo Patterns doc)
		pattern := fmt.Sprintf("%d@%s", txIndex, txID)
		ctx, cancel := context.WithTimeout(context.Background(), defaultKupoTimeout)
		matches, err := k.Matches(ctx, kugo.Pattern(pattern))
		cancel()
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "Invalid pattern!") || strings.Contains(errStr, "cannot unmarshal object into Go value of type []kugo.Match") {
				if !m.kupoInvalidPatternLogged {
					m.kupoInvalidPatternLogged = true
					if m.logger != nil {
						m.logger.Debug("Kupo does not support output-reference pattern, disabling input resolution", "error", err)
					}
				}
				m.kupoDisabled = true
				return resolvedInputs, nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("kupo matches query timed out after %v", defaultKupoTimeout)
			}
			return nil, fmt.Errorf("error fetching matches for input TxId: %s, Index: %d: %w", txID, txIndex, err)
		}
		for _, match := range matches {
			out, err := chainsync.NewResolvedTransactionOutput(match)
			if err != nil {
				return nil, err
			}
			resolvedInputs = append(resolvedInputs, out)
		}
		if len(matches) == 0 && m.logger != nil {
			m.logger.Debug("Kupo returned no matches for input; ensure Kupo is run with a pattern that indexes this output (e.g. --match \"*\")", "pattern", pattern)
		}
	}
	return resolvedInputs, nil
}
