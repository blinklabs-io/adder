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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SundaeSwap-finance/kugo"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/input/chainsync"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/internal/nodeconn"
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

// pollTx holds a transaction and its hash for one mempool poll.
type pollTx struct {
	hash string
	tx   ledger.Transaction
}

type Mempool struct {
	plugin.Base
	network         string
	networkMagic    uint32
	socketPath      string
	address         string
	ntcTcp          bool
	includeCbor     bool
	pollIntervalStr string
	pollInterval    time.Duration
	kupoUrl         string

	// connMu guards oConn. The connection is installed by setupConnection
	// and taken by Stop, which runs on the caller's goroutine, while the
	// poll loop reads it from a worker. Reach it only through conn,
	// setConn, takeConn and closeConn.
	connMu       sync.Mutex
	oConn        *ouroboros.Connection
	dialFamily   string
	dialAddress  string
	seenTxHashes map[string]struct{}

	kupoClient               *kugo.Client
	kupoDisabled             bool
	kupoInvalidPatternLogged bool
}

// conn returns the current node connection, or nil when there is none:
// before the first dial, or after Stop.
func (m *Mempool) conn() *ouroboros.Connection {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	return m.oConn
}

// setConn installs conn as the current node connection.
func (m *Mempool) setConn(conn *ouroboros.Connection) {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	m.oConn = conn
}

// takeConn clears the current node connection and returns it, so that
// exactly one of the racing callers gets a non-nil connection to close.
func (m *Mempool) takeConn() *ouroboros.Connection {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	conn := m.oConn
	m.oConn = nil
	return conn
}

// closeConn closes the current node connection, if there is one. The
// close runs outside the lock: it blocks until the connection's own
// goroutines are done, and those call back into this plugin.
func (m *Mempool) closeConn() error {
	conn := m.takeConn()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// New returns a new Mempool input plugin
func New(opts ...MempoolOptionFunc) *Mempool {
	m := &Mempool{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Role identifies this plugin as a pipeline input.
func (m *Mempool) Role() plugin.PluginType { return plugin.PluginTypeInput }

// Start connects to the node and starts polling. It is idempotent while
// running; restart with Stop followed by Start.
func (m *Mempool) Start() error {
	return m.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (m *Mempool) StartContext(ctx context.Context) error {
	return m.StartRun(ctx,
		plugin.BaseConfig{HasOutput: true},
		m.start,
		m.shutdownHooks(),
	)
}

func (m *Mempool) start(ctx context.Context) error {
	// Reset Kupo state on each start so configuration changes or temporary
	// errors don't permanently disable input resolution.
	m.kupoClient = nil
	m.kupoDisabled = false
	m.kupoInvalidPatternLogged = false

	if logger := m.Logger(); logger != nil {
		if m.kupoUrl == "" {
			logger.Info(
				"Kupo URL not set; inputs will be resolved from mempool only (chained txs). Set KUPO_URL or --input-mempool-kupo-url to also resolve on-chain inputs.",
			)
		} else {
			logger.Info(
				"Using Kupo for input resolution (on-chain); mempool chained txs resolved from poll",
				"url", m.kupoUrl,
			)
		}
	}

	if err := m.setupConnection(ctx); err != nil {
		return err
	}

	conn := m.conn()
	if conn == nil {
		return errors.New("mempool: connection setup left no connection")
	}
	conn.LocalTxMonitor().Client.Start()

	m.Go(m.pollLoop)
	return nil
}

// Stop cancels startup or polling and releases the node connection.
func (m *Mempool) Stop() error {
	return m.Shutdown(m.shutdownHooks())
}

func (m *Mempool) shutdownHooks() plugin.ShutdownHooks {
	return plugin.ShutdownHooks{
		BeforeWait: m.closeConn,
		AfterWait:  m.closeConn,
	}
}

func (m *Mempool) setupConnection(ctx context.Context) error {
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
			return errors.New(
				"address requires input-mempool-ntc-tcp=true for NtC over TCP",
			)
		}
	} else if m.socketPath != "" {
		m.dialFamily = "unix"
		m.dialAddress = m.socketPath
	} else {
		return errors.New("must specify input-mempool-socket-path or input-mempool-address")
	}
	if m.networkMagic == 0 {
		return errors.New(
			"must specify input-mempool-network or input-mempool-network-magic",
		)
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
	oConn, err := nodeconn.Dial(ctx, m.dialFamily, m.dialAddress,
		ouroboros.WithNetworkMagic(m.networkMagic),
		ouroboros.WithNodeToNode(false),
		ouroboros.WithKeepAlive(true),
		ouroboros.WithLocalTxMonitorConfig(cfg),
	)
	if err != nil {
		return err
	}
	m.setConn(oConn)
	if logger := m.Logger(); logger != nil {
		logger.Info("connected to node for mempool", "address", m.dialAddress)
	}

	// Capture the connection error channel from the local rather than
	// re-reading the field in the worker: Stop takes the connection, and
	// the worker would then have nothing to read from.
	connErrChan := oConn.ErrorChan()
	m.Go(func() {
		done := m.Done()
		for {
			select {
			case <-done:
				return
			case err, ok := <-connErrChan:
				if !ok {
					err = errors.New("mempool: connection closed")
				}
				m.Fail(err)
				return
			}
		}
	})
	return nil
}

func (m *Mempool) pollLoop() {
	done := m.Done()
	if m.pollInterval <= 0 {
		m.pollInterval = defaultPollInterval
	}
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			m.pollOnce()
		}
	}
}

func (m *Mempool) pollOnce() {
	done := m.Done()
	// Take a reference once. Stop can retire the connection at any point
	// in this poll, and re-reading the field would race that.
	conn := m.conn()
	if conn == nil {
		return
	}
	ltm := conn.LocalTxMonitor()
	if ltm == nil {
		// A connection negotiated without node-to-client has no local
		// tx-monitor protocol to poll.
		return
	}
	client := ltm.Client
	if client == nil {
		return
	}
	if err := client.Acquire(); err != nil {
		if logger := m.Logger(); logger != nil {
			logger.Warn("mempool acquire failed", "error", err)
		}
		return
	}
	defer func() {
		_ = client.Release()
	}()

	_, _, numTxs, err := client.GetSizes()
	if err != nil {
		if logger := m.Logger(); logger != nil {
			logger.Warn("mempool GetSizes failed", "error", err)
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
	var pollTxs []pollTx
	for {
		select {
		case <-done:
			return
		default:
		}
		txCbor, err := client.NextTx()
		if err != nil {
			if logger := m.Logger(); logger != nil {
				logger.Warn("mempool NextTx failed", "error", err)
			}
			return
		}
		if len(txCbor) == 0 {
			break
		}
		tx, err := m.parseTx(txCbor)
		if err != nil {
			if logger := m.Logger(); logger != nil {
				logger.Debug(
					"mempool skip tx parse error",
					"error",
					err,
					"cbor_len",
					len(txCbor),
				)
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

	// Build UTxO set from this poll's transactions so chained mempool txs can
	// resolve inputs (e.g. tx A spends an output of tx B, both in mempool).
	mempoolUtxo := m.buildMempoolUtxo(pollTxs)

	for _, p := range pollTxs {
		if _, seen := m.seenTxHashes[p.hash]; seen {
			continue
		}
		ctx := event.NewMempoolTransactionContext(p.tx, 0, m.networkMagic)
		payload := event.NewTransactionEventFromTx(p.tx, m.includeCbor)
		resolvedInputs, resolveErr := m.resolveTransactionInputs(
			p.tx,
			mempoolUtxo,
		)
		if len(resolvedInputs) > 0 {
			payload.ResolvedInputs = resolvedInputs
		}
		if logger := m.Logger(); resolveErr != nil && logger != nil {
			logger.Warn(
				"some transaction inputs could not be resolved; partial resolved inputs may be set",
				"error",
				resolveErr,
			)
		}
		evt := event.New(event.TypeTransaction, time.Now(), ctx, payload)
		// Emit gives up when the plugin is shutting down, which is when
		// the old select fell through to its done case and returned.
		if !m.Emit(evt) {
			return
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
	kugoLogger := logging.NewKugoCustomLoggerWithLogger(
		logging.GetLoggerForComponent("kupo"),
	)
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
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf(
			"health check failed with status code: %d",
			resp.StatusCode,
		)
	}
	m.kupoClient = k
	return k, nil
}

// buildMempoolUtxo builds a map from "txHash:outputIndex" to the transaction
// output so that chained mempool transactions (tx A spends an output of tx B,
// both in the same poll) can resolve inputs without requiring Kupo.
func (m *Mempool) buildMempoolUtxo(
	pollTxs []pollTx,
) map[string]ledger.TransactionOutput {
	utxo := make(map[string]ledger.TransactionOutput)
	for _, p := range pollTxs {
		txID := p.hash
		for idx, out := range p.tx.Outputs() {
			key := txID + ":" + strconv.Itoa(idx)
			utxo[key] = out
		}
	}
	return utxo
}

// resolveTransactionInputs resolves each input from the mempool UTxO set (chained
// txs) or Kupo (on-chain). It always returns whatever could be resolved. If any
// input failed to resolve (e.g. Kupo error), the second return is a non-nil error
// so the caller can log it; partial results are still returned.
func (m *Mempool) resolveTransactionInputs(
	tx ledger.Transaction,
	mempoolUtxo map[string]ledger.TransactionOutput,
) ([]ledger.TransactionOutput, error) {
	var resolvedInputs []ledger.TransactionOutput
	var resolveErrs []error
	for _, input := range tx.Inputs() {
		txID := input.Id().String()
		txIndex := int(input.Index())
		key := txID + ":" + strconv.Itoa(txIndex)

		// Resolve from mempool first (chained txs: both in same poll).
		if out, ok := mempoolUtxo[key]; ok {
			resolvedInputs = append(resolvedInputs, out)
			continue
		}

		// Fall back to Kupo for on-chain outputs.
		if m.kupoUrl == "" || m.kupoDisabled {
			continue
		}
		k, err := m.getKupoClient()
		if err != nil {
			resolveErrs = append(
				resolveErrs,
				fmt.Errorf("input %s:%d kupo client: %w", txID, txIndex, err),
			)
			continue
		}
		pattern := fmt.Sprintf("%d@%s", txIndex, txID)
		ctx, cancel := context.WithTimeout(
			context.Background(),
			defaultKupoTimeout,
		)
		matches, err := k.Matches(ctx, kugo.Pattern(pattern))
		cancel()
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "Invalid pattern!") ||
				strings.Contains(
					errStr,
					"cannot unmarshal object into Go value of type []kugo.Match",
				) {
				if !m.kupoInvalidPatternLogged {
					m.kupoInvalidPatternLogged = true
					if logger := m.Logger(); logger != nil {
						logger.Debug(
							"Kupo does not support output-reference pattern, disabling Kupo input resolution",
							"error",
							err,
						)
					}
				}
				m.kupoDisabled = true
				continue
			}
			resolveErrs = append(
				resolveErrs,
				fmt.Errorf("input %s:%d: %w", txID, txIndex, err),
			)
			continue
		}
		for _, match := range matches {
			out, err := chainsync.NewResolvedTransactionOutput(match)
			if err != nil {
				resolveErrs = append(
					resolveErrs,
					fmt.Errorf("input %s:%d match: %w", txID, txIndex, err),
				)
				continue
			}
			resolvedInputs = append(resolvedInputs, out)
		}
		if logger := m.Logger(); len(matches) == 0 && logger != nil {
			logger.Debug(
				"Kupo returned no matches for input; ensure Kupo is run with a pattern that indexes this output (e.g. --match \"*\")",
				"pattern",
				pattern,
			)
		}
	}
	if len(resolveErrs) > 0 {
		return resolvedInputs, errors.Join(resolveErrs...)
	}
	return resolvedInputs, nil
}

var _ plugin.ManagedPlugin = (*Mempool)(nil)
