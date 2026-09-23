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
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/blinklabs-io/adder/plugin"
	ouroboros "github.com/blinklabs-io/gouroboros"
	syncpb "github.com/utxorpc/go-codegen/utxorpc/v1beta/sync"
	watchpb "github.com/utxorpc/go-codegen/utxorpc/v1beta/watch"
	sdk "github.com/utxorpc/go-sdk"
)

const (
	modeFollowTip = "follow-tip"
	modeWatchTx   = "watch-tx"

	maxReconnectDelay = 60 * time.Second
	eventChanBuffer   = 2048
)

// Utxorpc is an input plugin that consumes UTxO RPC streaming endpoints
// and emits adder events.
type Utxorpc struct {
	plugin.Base

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
	client *sdk.UtxorpcClient
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

// Role identifies this plugin as a pipeline input.
func (u *Utxorpc) Role() plugin.PluginType { return plugin.PluginTypeInput }

// Start begins streaming from the configured UTxO RPC endpoint.
func (u *Utxorpc) Start() error {
	return u.StartContext(context.Background())
}

// StartContext starts the plugin with ctx governing setup and run operations.
// Call Stop to wait for workers and release resources, including after cancellation.
func (u *Utxorpc) StartContext(ctx context.Context) error {
	return u.StartRun(ctx,
		plugin.BaseConfig{
			HasOutput:    true,
			OutputBuffer: eventChanBuffer,
		},
		u.start,
		plugin.ShutdownHooks{},
	)
}

func (u *Utxorpc) start(ctx context.Context) error {
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

	if u.intersectPoint != "" && u.intersectTip {
		u.intersectTip = false
		if logger := u.Logger(); logger != nil {
			logger.Warn(
				"intersect-point is set, overriding intersect-tip to false",
			)
		}
	}

	headers := map[string]string{}
	if u.apiKeyHeader != "" && u.apiKey != "" {
		headers[u.apiKeyHeader] = u.apiKey
	}

	u.client = sdk.NewClient(
		sdk.WithBaseUrl(u.url),
		sdk.WithHeaders(headers),
	)

	u.Go(u.run)

	if logger := u.Logger(); logger != nil {
		logger.Info(
			"started utxorpc input",
			"url", u.url,
			"mode", u.mode,
			"hasApiKey", u.apiKey != "",
		)
	}
	return nil
}

// Stop terminates the stream and closes channels. Idempotent.
func (u *Utxorpc) Stop() error {
	return u.Shutdown(plugin.ShutdownHooks{})
}

func (u *Utxorpc) run() {
	done := u.Done()
	backoff := time.Second
	for {
		select {
		case <-done:
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

		if !u.autoReconnect {
			if err == nil {
				err = errors.New("utxorpc: stream ended")
			}
			u.Fail(err)
			return
		}
		if err == nil {
			backoff = time.Second
		} else if !u.SendError(err) {
			return
		}

		if logger := u.Logger(); logger != nil {
			logger.Warn("utxorpc stream ended, reconnecting", "error", err)
		}

		select {
		case <-done:
			return
		case <-time.After(backoff):
		}
		// Only grow backoff on failures; successful sessions reset above.
		if err != nil {
			backoff *= 2
			if backoff > maxReconnectDelay {
				backoff = maxReconnectDelay
			}
		}
	}
}

func (u *Utxorpc) runFollowTipOnce() error {
	ctx, cancel := context.WithCancel(u.Context())
	defer cancel()
	done := u.Done()
	if ctx.Err() != nil {
		return nil
	}

	req := connect.NewRequest(&syncpb.FollowTipRequest{
		Intersect: u.syncIntersectRefs(),
	})
	stream, err := u.client.FollowTipWithContext(ctx, req)
	if err != nil {
		return fmt.Errorf("utxorpc FollowTip: %w", err)
	}
	defer stream.Close()

	for {
		select {
		case <-done:
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
			// Emit gives up when the plugin is shutting down, which is
			// when the old select took its done case.
			if !u.Emit(evt) {
				return nil
			}
		}
	}
}

func (u *Utxorpc) runWatchTxOnce() error {
	ctx, cancel := context.WithCancel(u.Context())
	defer cancel()
	done := u.Done()
	if ctx.Err() != nil {
		return nil
	}

	req := connect.NewRequest(&watchpb.WatchTxRequest{
		Intersect: u.watchIntersectRefs(),
	})
	stream, err := u.client.WatchTxWithContext(ctx, req)
	if err != nil {
		return fmt.Errorf("utxorpc WatchTx: %w", err)
	}
	defer stream.Close()

	for {
		select {
		case <-done:
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
			if logger := u.Logger(); logger != nil {
				logger.Debug(
					"utxorpc WatchTx idle",
					"slot", idle.GetSlot(),
					"hash", hex.EncodeToString(idle.GetHash()),
				)
			}
			continue
		}

		evts, err := mapWatchTxResponse(resp, u.networkMagic)
		if err != nil {
			return fmt.Errorf("utxorpc WatchTx: %w", err)
		}
		for _, evt := range evts {
			// Emit gives up when the plugin is shutting down, which is
			// when the old select took its done case.
			if !u.Emit(evt) {
				return nil
			}
		}
	}
}

// intersectPoint holds slot/hash parsed from intersect configuration.
// Sync and watch APIs use different protobuf packages for BlockRef but identical
// field semantics; parse once and map to each request type.
type intersectPoint struct {
	slot uint64
	hash []byte
}

// parseIntersectPoints parses intersectPoint configuration. Format: "slot.hash"
// or "slot1.hash1,slot2.hash2".
func (u *Utxorpc) parseIntersectPoints() []intersectPoint {
	if u.intersectPoint == "" {
		return nil
	}
	pointsSlice := strings.Split(u.intersectPoint, ",")
	out := make([]intersectPoint, 0, len(pointsSlice))
	for _, point := range pointsSlice {
		parts := strings.SplitN(strings.TrimSpace(point), ".", 2)
		if len(parts) != 2 {
			if logger := u.Logger(); logger != nil {
				logger.Warn("ignoring invalid intersect point", "point", point)
			}
			continue
		}
		slot, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			if logger := u.Logger(); logger != nil {
				logger.Warn(
					"ignoring intersect point: invalid slot",
					"point",
					point,
					"error",
					err,
				)
			}
			continue
		}
		hashBytes, err := hex.DecodeString(parts[1])
		if err != nil {
			if logger := u.Logger(); logger != nil {
				logger.Warn(
					"ignoring intersect point: invalid hash",
					"point",
					point,
					"error",
					err,
				)
			}
			continue
		}
		out = append(out, intersectPoint{slot: slot, hash: hashBytes})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (u *Utxorpc) syncIntersectRefs() []*syncpb.BlockRef {
	pts := u.parseIntersectPoints()
	if len(pts) == 0 {
		return nil
	}
	refs := make([]*syncpb.BlockRef, len(pts))
	for i, p := range pts {
		refs[i] = &syncpb.BlockRef{Slot: p.slot, Hash: p.hash}
	}
	return refs
}

func (u *Utxorpc) watchIntersectRefs() []*watchpb.BlockRef {
	pts := u.parseIntersectPoints()
	if len(pts) == 0 {
		return nil
	}
	refs := make([]*watchpb.BlockRef, len(pts))
	for i, p := range pts {
		refs[i] = &watchpb.BlockRef{Slot: p.slot, Hash: p.hash}
	}
	return refs
}

var _ plugin.ManagedPlugin = (*Utxorpc)(nil)
