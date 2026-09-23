//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartupIntegration(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	_, occupiedPort, err := net.SplitHostPort(occupied.Addr().String())
	require.NoError(t, err)
	for _, tc := range []struct {
		name    string
		flags   []string
		message string
	}{
		{name: "output-level-without-log-output", flags: []string{"--output", "webhook", "--output-log-level", "invalid"}, message: "level must be debug, info, warn, or error"},
		{name: "output-file-open-failure", flags: []string{"--output-log-path", "missing-directory/events.jsonl"}, message: "missing-directory/events.jsonl"},
		{name: "api-port-occupied", flags: []string{"--api-port", occupiedPort}, message: "address already in use"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fixture{release: make(chan struct{})}
			close(f.release)
			p := launchAdder(t, f, settings{}, tc.flags...)
			select {
			case <-p.done:
				require.Error(t, p.err, "invalid startup must fail")
			case <-time.After(5 * time.Second):
				t.Fatal("startup failure left Adder running")
			}
			require.Contains(t, p.read(t, "stderr.log"), tc.message)
			require.NotContains(t, p.read(t, "stderr.log"), "DATA RACE")
			require.NotContains(t, p.read(t, "stderr.log"), "panic:")
			require.Empty(t, p.read(t, "stdout.log"))
			require.Zero(t, f.attempts.Load(), "invalid startup must not consume input")
			if tc.name == "output-file-open-failure" {
				// The API starts before plugins; a failed output startup must
				// release its listener as well as terminating the process.
				listener, err := net.Listen("tcp", p.apiURL[len("http://"):])
				require.NoError(t, err)
				require.NoError(t, listener.Close())
			}
		})
	}
}
