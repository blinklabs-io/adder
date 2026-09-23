//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"

	"connectrpc.com/connect"
	cardanopb "github.com/utxorpc/go-codegen/utxorpc/v1beta/cardano"
	syncpb "github.com/utxorpc/go-codegen/utxorpc/v1beta/sync"
	"github.com/utxorpc/go-codegen/utxorpc/v1beta/sync/syncconnect"
)

type fixture struct {
	syncconnect.UnimplementedSyncServiceHandler
	release   chan struct{}
	fail      bool
	failFirst int32
	attempts  atomic.Int32
}

func (f *fixture) FollowTip(ctx context.Context, _ *connect.Request[syncpb.FollowTipRequest], stream *connect.ServerStream[syncpb.FollowTipResponse]) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.release:
	}
	if attempt := f.attempts.Add(1); f.fail || attempt <= f.failFirst {
		return connect.NewError(connect.CodeInternal, errors.New("integration fixture failure"))
	}
	block := &syncpb.AnyChainBlock{Chain: &syncpb.AnyChainBlock_Cardano{Cardano: &cardanopb.Block{
		Header: &cardanopb.BlockHeader{Slot: 100, Height: 50, Hash: bytes.Repeat([]byte{1}, 32)},
		Body: &cardanopb.BlockBody{Tx: []*cardanopb.Tx{{
			Hash: bytes.Repeat([]byte{2}, 32),
			Fee:  &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 200000}},
			Proposals: []*cardanopb.GovernanceActionProposal{{
				Deposit:       &cardanopb.BigInt{BigInt: &cardanopb.BigInt_Int{Int: 500000000}},
				RewardAccount: append([]byte{0xe1}, make([]byte, 28)...),
				GovAction: &cardanopb.GovernanceAction{GovernanceAction: &cardanopb.GovernanceAction_InfoAction{
					InfoAction: &cardanopb.InfoAction{},
				}},
			}},
		}}},
	}}}
	for _, response := range []*syncpb.FollowTipResponse{
		{Action: &syncpb.FollowTipResponse_Apply{Apply: block}},
		{Action: &syncpb.FollowTipResponse_Undo{Undo: block}},
	} {
		if err := stream.Send(response); err != nil {
			return err
		}
	}
	// Keep the stream open so EOF cannot masquerade as a shutdown failure.
	<-ctx.Done()
	return ctx.Err()
}
