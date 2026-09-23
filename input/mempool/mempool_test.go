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

package mempool

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/plugin"
	"github.com/blinklabs-io/adder/plugintest"
	ouroboros "github.com/blinklabs-io/gouroboros"
	ouroboros_mock "github.com/blinklabs-io/ouroboros-mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockNodeConnection returns a real *ouroboros.Connection whose peer is
// an in-memory mock that answers a node-to-client handshake and nothing
// else. The local tx-monitor protocol is negotiated, so pollOnce gets past
// its nil guards and reaches the connection the way it does in production.
func newMockNodeConnection(t *testing.T) *ouroboros.Connection {
	t.Helper()
	mockConn := ouroboros_mock.NewConnection(
		ouroboros_mock.ProtocolRoleClient,
		[]ouroboros_mock.ConversationEntry{
			ouroboros_mock.ConversationEntryHandshakeRequestGeneric,
			ouroboros_mock.ConversationEntryHandshakeNtCResponse,
		},
	)
	conn, err := ouroboros.New(
		ouroboros.WithConnection(mockConn),
		ouroboros.WithNetworkMagic(ouroboros_mock.MockNetworkMagic),
	)
	require.NoError(t, err)
	return conn
}

// requireConnClosed blocks until conn has finished shutting down. A
// connection closes the error channel it owns as the last step of
// shutdown, so a closed error channel is the observable proof that the
// connection is gone rather than merely unreferenced.
func requireConnClosed(t *testing.T, conn *ouroboros.Connection) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-conn.ErrorChan():
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the connection is still open after Stop returned")
		}
	}
}

// TestStopDuringPollDoesNotRaceOnTheConnection pins the lock around oConn.
// pollOnce reads the connection from a worker while Stop retires it from
// the caller's goroutine, with no happens-before edge between them: Stop
// does that work in BeforeWait, which runs before the wait that would
// otherwise order the two. Unguarded, that is a data race by the Go memory
// model, and the read can also observe a half-written interface value.
//
// Reverting the accessors to a bare field read and write fails this test
// under -race.
func TestStopDuringPollDoesNotRaceOnTheConnection(t *testing.T) {
	// Which of Stop's steps the poll lands in is a race, so repeat.
	const rounds = 20
	for i := range rounds {
		m := &Mempool{pollInterval: time.Hour}
		plugintest.StartBase(t, &m.Base, plugin.BaseConfig{HasOutput: true})
		m.setConn(newMockNodeConnection(t))

		polling := make(chan struct{})
		m.Go(func() {
			close(polling)
			for {
				select {
				case <-m.Done():
					return
				default:
				}
				// Reads the connection. Closing it from Stop is what
				// unblocks the acquire and lets this loop notice Done.
				m.pollOnce()
			}
		})
		<-polling

		stopped := make(chan error, 1)
		go func() { stopped <- m.Stop() }()
		select {
		case err := <-stopped:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: Stop deadlocked waiting for the poll", i)
		}
	}
}

// TestStopClosesAConnectionInstalledDuringTheWait pins the AfterWait close.
// BeforeWait sees nothing when a dial is still in flight: setupConnection
// installs its connection afterwards, Stop returns nil, and the node
// connection and its goroutines stay live behind a plugin whose channels
// are closing.
//
// Dropping the AfterWait hook fails this test — the connection's error
// channel never closes, so requireConnClosed times out.
func TestStopClosesAConnectionInstalledDuringTheWait(t *testing.T) {
	m := &Mempool{pollInterval: time.Hour}
	plugintest.StartBase(t, &m.Base, plugin.BaseConfig{HasOutput: true})

	conn := newMockNodeConnection(t)
	// A tracked worker standing in for a dial that was in flight when Stop
	// ran. It installs the connection after the shutdown signal, which is
	// exactly the window BeforeWait cannot cover.
	m.Go(func() {
		<-m.Done()
		// Let Stop get past BeforeWait and into the wait this worker holds
		// open, so the connection lands in the uncovered window rather
		// than racing BeforeWait for it.
		time.Sleep(200 * time.Millisecond)
		m.setConn(conn)
	})

	require.NoError(t, m.Stop())

	requireConnClosed(t, conn)
	assert.Nil(t, m.conn(), "Stop must not leave a connection installed")
}

// TestFailedStartDoesNotLeaveThePluginRunning pins that Start unwinds
// itself. Init marks the plugin running before the connection is set up,
// so returning the error on its own would hand back a plugin that reports
// Running with nothing running in it.
func TestFailedStartDoesNotLeaveThePluginRunning(t *testing.T) {
	// No socket path and no address: setupConnection rejects it before it
	// touches the network.
	m := &Mempool{}

	require.Error(t, m.Start())

	assert.False(t, m.Running(),
		"a failed Start must unwind the Init it performed")
	assert.Nil(t, m.conn(), "a failed Start must not keep a connection")
	select {
	case <-m.Done():
	default:
		t.Fatal("the shutdown signal is still open after a failed Start")
	}
}

func TestStopCancelsInitialConnectionSetup(t *testing.T) {
	listener, err := net.ListenTCP(
		"tcp",
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
	)
	require.NoError(t, err)
	defer listener.Close()
	require.NoError(t, listener.SetDeadline(time.Now().Add(5*time.Second)))
	m := New(
		WithAddress(listener.Addr().String()),
		WithNtcTcp(true),
		WithNetworkMagic(ouroboros_mock.MockNetworkMagic),
	)
	started := make(chan error, 1)
	go func() { started <- m.Start() }()
	peer, err := listener.Accept()
	require.NoError(t, err)
	defer peer.Close()
	// The peer never answers the handshake. Stop must cancel it rather
	// than wait for the network or protocol timeout.
	stopped := make(chan error, 1)
	go func() { stopped <- m.Stop() }()
	select {
	case err := <-started:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("startup was not canceled")
	}
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish")
	}
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = io.Copy(io.Discard, peer)
	require.NoError(t, err, "the canceled handshake must close its socket")
	assert.False(t, m.Running())
	assert.Nil(t, m.conn())
	assert.Nil(t, m.OutputChan())
	require.NoError(t, m.Stop())
}

// TestStopReleasesRunningPollLoop is a regression test for a deadlock that
// was live in the pre-Base code. Stop() closed m.doneChan and then set the
// field to nil before waiting for the workers, while pollLoop re-read
// m.doneChan inside its select. select re-evaluates its channel operands
// every time it runs, so a pollLoop that reached the select after Stop had
// nil'd the field saw a nil channel, which is never ready. The loop then
// went on ticking forever and Stop()'s wg.Wait() never returned. The window
// is the whole of pollOnce, which in production is an in-flight poll of the
// node.
//
// Base.signalStop leaves doneChan in place, closed, and pollLoop captures it
// once at the top, so the shutdown signal is observable no matter when the
// loop next reaches its select.
func TestStopReleasesRunningPollLoop(t *testing.T) {
	// Stopping a pollLoop that is parked in its select was always safe:
	// the close wakes it. The ordering that hung is a pollLoop caught
	// between iterations, so drive it with a tick interval short enough
	// that it is spinning rather than parked, and repeat, since which of
	// the two states Stop lands in is a race. The pre-Base code hung on
	// roughly one round in ten under -race.
	const rounds = 50
	for i := range rounds {
		m := &Mempool{pollInterval: time.Microsecond}
		plugintest.StartBase(t, &m.Base, plugin.BaseConfig{HasOutput: true})
		// pollOnce returns immediately with no connection, so the loop
		// body is just the tick.
		m.Go(m.pollLoop)
		time.Sleep(2 * time.Millisecond)

		stopped := make(chan error, 1)
		go func() { stopped <- m.Stop() }()
		select {
		case err := <-stopped:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: Stop() deadlocked waiting for pollLoop", i)
		}
	}
}

// TestStopIsIdempotent covers the sync.Once that Base.Shutdown replaced:
// a second Stop() must not double-close the plugin channels.
func TestStopIsIdempotent(t *testing.T) {
	m := &Mempool{pollInterval: time.Hour}
	plugintest.StartBase(t, &m.Base, plugin.BaseConfig{HasOutput: true})
	m.Go(m.pollLoop)
	require.NoError(t, m.Stop())
	require.NoError(t, m.Stop())
}

func TestStartPreservesRunningConnection(t *testing.T) {
	m := New()
	plugintest.StartBase(t, &m.Base, plugin.BaseConfig{HasOutput: true})
	conn := newMockNodeConnection(t)
	m.setConn(conn)
	output, errs := m.OutputChan(), m.ErrorChan()
	require.NoError(t, m.Start())
	assert.Same(t, conn, m.conn())
	assert.Equal(t, output, m.OutputChan())
	assert.Equal(t, errs, m.ErrorChan())
	require.NoError(t, m.Stop())
	requireConnClosed(t, conn)
}
