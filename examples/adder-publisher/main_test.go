//go:build !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublisherSignalProcess(t *testing.T) {
	if os.Getenv("ADDER_TEST_PUBLISHER") != "1" {
		return
	}
	main()
}

func TestPublisherSignalsCancelNodeHandshake(t *testing.T) {
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			// Keep the socket path below Unix-domain pathname length limits.
			dir, err := os.MkdirTemp("", "adder-signal-")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
			path := filepath.Join(dir, "node.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			require.NoError(t, listener.SetDeadline(time.Now().Add(10*time.Second)))

			command := exec.Command(os.Args[0], "-test.run=^TestPublisherSignalProcess$")
			command.Env = []string{
				"ADDER_TEST_PUBLISHER=1",
				"CARDANO_NODE_SOCKET_PATH=" + path,
				"CARDANO_NODE_MAGIC=764824073",
			}
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			require.NoError(t, command.Start())
			done := make(chan struct{})
			var waitErr error
			go func() {
				waitErr = command.Wait()
				close(done)
			}()
			t.Cleanup(func() {
				_ = command.Process.Kill()
				<-done
			})

			peer, err := listener.AcceptUnix()
			require.NoError(t, err)
			t.Cleanup(func() { _ = peer.Close() })
			require.NoError(t, peer.SetReadDeadline(time.Now().Add(10*time.Second)))
			_, err = io.ReadFull(peer, make([]byte, 1))
			require.NoError(t, err)
			require.NoError(t, command.Process.Signal(signal))
			select {
			case <-done:
				require.NoError(t, waitErr, "%s", output.String())
				require.NotContains(t, output.String(), "failed to start pipeline")
			case <-time.After(3 * time.Second):
				t.Fatal("signal did not cancel the blocked node handshake")
			}
		})
	}
}
