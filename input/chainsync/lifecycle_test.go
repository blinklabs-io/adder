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

package chainsync

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	nodefixture "github.com/blinklabs-io/adder/internal/plugintest"
	"github.com/blinklabs-io/adder/plugintest"
	ouroboros "github.com/blinklabs-io/gouroboros"
	cs "github.com/blinklabs-io/gouroboros/protocol/chainsync"
	"github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/stretchr/testify/require"
)

func TestLifecycleContract(t *testing.T) {
	plugintest.Lifecycle(
		t,
		New(
			WithAddress(nodefixture.Node(t)),
			WithNtcTcp(true),
			WithNetworkMagic(42),
		),
	)
}

func TestFailedStartContract(t *testing.T) { plugintest.FailedStart(t, New()) }

func TestStopCancelsConnectionSetup(t *testing.T) {
	for _, phase := range []string{"handshake", "intersection"} {
		t.Run(phase, func(t *testing.T) {
			listener, err := net.ListenTCP(
				"tcp",
				&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
			)
			require.NoError(t, err)
			defer listener.Close()
			require.NoError(
				t,
				listener.SetDeadline(time.Now().Add(5*time.Second)),
			)
			c := New(
				WithAddress(listener.Addr().String()),
				WithNtcTcp(true),
				WithNetworkMagic(42),
			)
			started := make(chan error, 1)
			go func() { started <- c.Start() }()
			peer, err := listener.Accept()
			require.NoError(t, err)
			defer peer.Close()
			var server *ouroboros.Connection
			if phase == "intersection" {
				entered := make(chan struct{})
				server, err = ouroboros.New(
					ouroboros.WithConnection(peer),
					ouroboros.WithServer(true),
					ouroboros.WithNetworkMagic(42),
					ouroboros.WithChainSyncConfig(cs.NewConfig(
						cs.WithFindIntersectFunc(
							func(ctx cs.CallbackContext, _ []common.Point) (common.Point, cs.Tip, error) {
								close(entered)
								<-ctx.ConnectionDoneChan
								return common.Point{}, cs.Tip{}, errors.New(
									"closed",
								)
							},
						),
					)),
				)
				require.NoError(t, err)
				defer server.Close()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("client did not request an intersection")
				}
			}
			stopped := make(chan error, 1)
			go func() { stopped <- c.Stop() }()
			select {
			case err := <-started:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not cancel startup")
			}
			select {
			case err := <-stopped:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not finish cleanup")
			}
			require.Nil(t, c.conn())
			require.False(t, c.Running())
			if server == nil {
				require.NoError(
					t,
					peer.SetReadDeadline(time.Now().Add(time.Second)),
				)
				_, err = io.Copy(io.Discard, peer)
				require.NoError(t, err)
			} else {
				requireConnClosed(t, server)
			}
		})
	}
}
