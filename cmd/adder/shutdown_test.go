//go:build !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/examples/plugins"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type stopErrorInput struct{ *plugins.TickerInput }

func (p *stopErrorInput) Stop() error {
	return errors.Join(p.TickerInput.Stop(), errors.New("test stop failure"))
}

func TestCLIStreamShutdownProcess(t *testing.T) {
	if os.Getenv("ADDER_TEST_STREAM_SHUTDOWN") != "1" {
		return
	}
	plugin.Register(plugin.PluginEntry{
		Type: plugin.PluginTypeInput, Name: "shutdown-source",
		NewFromOptionsFunc: func(plugin.Options) (plugin.ManagedPlugin, error) {
			p, err := plugins.NewTickerInput(20 * time.Millisecond)
			if err != nil {
				return nil, err
			}
			if os.Getenv("ADDER_TEST_STOP_ERROR") == "true" {
				return &stopErrorInput{p}, nil
			}
			return p, nil
		},
	})
	rootCmd.SetArgs([]string{
		"--input", "shutdown-source", "--output", "log",
		"--api-address", "127.0.0.1", "--api-port", os.Getenv("ADDER_TEST_API_PORT"),
	})
	err := rootCmd.Execute()
	if os.Getenv("ADDER_TEST_STOP_ERROR") == "true" {
		require.ErrorContains(t, err, "test stop failure")
	} else {
		require.NoError(t, err)
	}
}

func TestCLIShutdownWithOpenSSE(t *testing.T) {
	for _, tc := range []struct {
		name      string
		signal    os.Signal
		stopError bool
	}{
		{name: "SIGINT", signal: os.Interrupt},
		{name: "SIGTERM", signal: syscall.SIGTERM},
		{name: "plugin-stop-error", signal: syscall.SIGTERM, stopError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			address := listener.Addr().String()
			_, port, err := net.SplitHostPort(address)
			require.NoError(t, err)
			require.NoError(t, listener.Close())

			logPath := filepath.Join(t.TempDir(), "child.log")
			logFile, err := os.Create(logPath)
			require.NoError(t, err)
			defer logFile.Close()
			command := exec.Command(os.Args[0], "-test.run=^TestCLIStreamShutdownProcess$")
			command.Env = []string{
				"ADDER_TEST_STREAM_SHUTDOWN=1", "ADDER_TEST_API_PORT=" + port,
				"ADDER_TEST_STOP_ERROR=" + strconv.FormatBool(tc.stopError),
			}
			for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
				if value, ok := os.LookupEnv(key); ok {
					command.Env = append(command.Env, key+"="+value)
				}
			}
			command.Stdout, command.Stderr = logFile, logFile
			require.NoError(t, command.Start())
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = command.Wait(); close(done) }()
			t.Cleanup(func() { _ = command.Process.Kill(); <-done })
			require.Eventually(t, func() bool {
				data, readErr := os.ReadFile(logPath)
				return readErr == nil && strings.Contains(string(data), "Adder started, waiting for shutdown signal")
			}, 5*time.Second, 10*time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/events?replay=false", nil)
			require.NoError(t, err)
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			line, err := bufio.NewReader(response.Body).ReadString('\n')
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(line, "data: "))

			require.NoError(t, command.Process.Signal(tc.signal))
			select {
			case <-done:
				data, readErr := os.ReadFile(logPath)
				require.NoError(t, readErr)
				require.NoError(t, waitErr, "%s", data)
				require.NotContains(t, string(data), "failed to stop API")
			case <-time.After(3 * time.Second):
				t.Fatal("CLI shutdown waited for the SSE client to disconnect")
			}
		})
	}
}
