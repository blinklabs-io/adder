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

// Package plugintest provides repository-specific node fixtures.
package plugintest

import (
	"context"
	"net"
	"sync"
	"testing"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/blinklabs-io/gouroboros/protocol/chainsync"
	"github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/require"
)

// Node serves local node-to-client handshakes and an idle chainsync stream.
func Node(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	var clients sync.WaitGroup
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			clients.Go(func() {
				defer raw.Close()
				stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
				defer stop()
				conn, err := ouroboros.New(
					ouroboros.WithConnection(raw),
					ouroboros.WithServer(true),
					ouroboros.WithNetworkMagic(42),
					ouroboros.WithChainSyncConfig(chainsync.NewConfig(
						chainsync.WithFindIntersectFunc(func(
							chainsync.CallbackContext, []common.Point,
						) (common.Point, chainsync.Tip, error) {
							return common.Point{}, chainsync.Tip{}, nil
						}),
						chainsync.WithRequestNextFunc(
							func(c chainsync.CallbackContext) error {
								return c.Server.AwaitReply()
							},
						),
					)),
				)
				if err != nil {
					return
				}
				defer conn.Close()
				for range conn.ErrorChan() {
				}
			})
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		<-accepted
		clients.Wait()
	})
	return listener.Addr().String()
}
