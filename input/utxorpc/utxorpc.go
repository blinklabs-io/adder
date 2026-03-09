// Copyright 2026 Blink Labs Software
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

package utxorpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
	syncpb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/sync"
	watchpb "github.com/utxorpc/go-codegen/utxorpc/v1alpha/watch"
	sdk "github.com/utxorpc/go-sdk"
)

const (
	modeFollowTip = "follow-tip"
	modeWatchTx   = "watch-tx"

	maxReconnectDelay = 60 * time.Second
)

// Utxorpc is an input plugin that consumes UTxO RPC streaming endpoints
// and emits adder events.
type Utxorpc struct {
	logger plugin.Logger

	// Configuration
	url            string
	mode           string
	network        string
	apiKeyHeader   string
	apiKey         string
	intersectTip   bool
	intersectPoint string
	autoReconnect  bool
	includeCbor    bool

	// Resolved at Start()
	networkMagic uint32

	// Runtime
	client    *sdk.UtxorpcClient
	eventChan chan event.Event
	errorChan chan error
	doneChan  chan struct{}
	wg        sync.WaitGroup
	stopOnce  sync.Once
}

// New returns a new Utxorpc plugin with the given options applied.
func New(options ...UtxoRpcOptionFunc) *Utxorpc {
	u := &Utxorpc{
		mode:          modeFollowTip,
		intersectTip:  true,
		autoReconnect: true,
	}
	for _, opt := range options {
		opt(u)
	}
	return u
}

// Start begins streaming from the configured UTxO RPC endpoint.
func (u *Utxorpc) Start() error {
	if u.url == "" {
		return errors.New("utxorpc: url must be configured")
	}

	if u.network != "" {
		net, ok := ouroboros.NetworkByName(u.network)
		if !ok {
			return fmt.Errorf("utxorpc: unknown network: %s", u.network)
		}
		u.networkMagic = net.NetworkMagic
	}

	u.stopOnce = sync.Once{}
	if u.doneChan != nil {
		close(u.doneChan)
		u.wg.Wait()
	}
	if u.eventChan == nil {
		u.eventChan = make(chan event.Event, 10)
	}
	if u.errorChan == nil {
		u.errorChan = make(chan error, 1)
	}
	u.doneChan = make(chan struct{})

	headers := map[string]string{}
	if u.apiKeyHeader != "" && u.apiKey != "" {
		headers[u.apiKeyHeader] = u.apiKey
	}
	u.logger.Info("starting utxorpc input", "url", u.url, "headers",
		headers[u.apiKeyHeader], "apiKey", u.apiKey, "apiKeyHeader", u.apiKeyHeader)

	u.client = sdk.NewClient(
		sdk.WithBaseUrl(u.url),
		sdk.WithHeaders(headers),
	)

	go u.run()

	if u.logger != nil {
		u.logger.Info(
			"started utxorpc input",
			"url", u.url,
			"mode", u.mode,
		)
	}
	return nil
}

// Stop terminates the stream and closes channels.
func (u *Utxorpc) Stop() error {
	u.stopOnce.Do(func() {
		if u.doneChan != nil {
			close(u.doneChan)
			u.doneChan = nil
		}
		u.wg.Wait()
		if u.eventChan != nil {
			close(u.eventChan)
			u.eventChan = nil
		}
		if u.errorChan != nil {
			close(u.errorChan)
			u.errorChan = nil
		}
	})
	return nil
}

// ErrorChan returns the plugin's error channel.
func (u *Utxorpc) ErrorChan() <-chan error {
	return u.errorChan
}

// InputChan always returns nil (input-only plugin).
func (u *Utxorpc) InputChan() chan<- event.Event {
	return nil
}

// OutputChan returns the output event channel.
func (u *Utxorpc) OutputChan() <-chan event.Event {
	return u.eventChan
}

func (u *Utxorpc) run() {
	backoff := time.Second
	for {
		select {
		case <-u.doneChan:
			return
		default:
		}

		var err error
		switch u.mode {
		case modeFollowTip, "":
			err = u.runFollowTipOnce()
		case modeWatchTx:
			err = u.runWatchTxOnce()
		default:
			err = fmt.Errorf("utxorpc: unknown mode %q", u.mode)
		}

		if err == nil {
			// Stream ended cleanly; exit unless asked to reconnect.
			if !u.autoReconnect {
				return
			}
		} else if u.errorChan != nil {
			select {
			case <-u.doneChan:
				return
			case u.errorChan <- err:
			default:
			}
		}

		if !u.autoReconnect {
			return
		}
		if u.logger != nil {
			u.logger.Warn("utxorpc stream ended, reconnecting", "error", err)
		}

		select {
		case <-u.doneChan:
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxReconnectDelay {
			backoff = maxReconnectDelay
		}
	}
}

func (u *Utxorpc) runFollowTipOnce() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := connect.NewRequest(&syncpb.FollowTipRequest{})
	// Intersect configuration is currently left to server defaults when unset.
	stream, err := u.client.FollowTipWithContext(ctx, req)
	if err != nil {
		return fmt.Errorf("utxorpc FollowTip: %w", err)
	}
	defer stream.Close()

	for {
		select {
		case <-u.doneChan:
			return nil
		default:
		}

		ok := stream.Receive()
		if !ok {
			return stream.Err()
		}
		resp := stream.Msg()
		if resp == nil {
			continue
		}

		evts, err := mapFollowTipResponse(resp, u.includeCbor, u.networkMagic)
		if err != nil {
			return fmt.Errorf("utxorpc FollowTip: %w", err)
		}
		for _, evt := range evts {
			select {
			case <-u.doneChan:
				return nil
			case u.eventChan <- evt:
			}
		}
	}
}

func (u *Utxorpc) runWatchTxOnce() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := connect.NewRequest(&watchpb.WatchTxRequest{})
	stream, err := u.client.WatchTxWithContext(ctx, req)
	if err != nil {
		return fmt.Errorf("utxorpc WatchTx: %w", err)
	}
	defer stream.Close()

	for {
		select {
		case <-u.doneChan:
			return nil
		default:
		}

		ok := stream.Receive()
		if !ok {
			return stream.Err()
		}
		resp := stream.Msg()
		if resp == nil {
			continue
		}

		if idle := resp.GetIdle(); idle != nil {
			if u.logger != nil {
				u.logger.Debug(
					"utxorpc WatchTx idle",
					"slot", idle.GetSlot(),
					"hash", fmt.Sprintf("%x", idle.GetHash()),
				)
			}
			continue
		}

		evts, err := mapWatchTxResponse(resp, u.networkMagic)
		if err != nil {
			return fmt.Errorf("utxorpc WatchTx: %w", err)
		}
		for _, evt := range evts {
			select {
			case <-u.doneChan:
				return nil
			case u.eventChan <- evt:
			}
		}
	}
}
