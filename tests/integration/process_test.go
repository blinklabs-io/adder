//go:build localintegration && !windows

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/utxorpc/go-codegen/utxorpc/v1beta/sync/syncconnect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type process struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	dir    string
	apiURL string
}

type settings struct {
	yaml string
	env  []string
}

func startAdder(t *testing.T, f *fixture, flags ...string) *process {
	t.Helper()
	p := launchAdder(t, f, settings{}, flags...)
	p.healthy(t)
	return p
}

func launchAdder(t *testing.T, f *fixture, config settings, flags ...string) *process {
	t.Helper()
	binary := os.Getenv("ADDER_INTEGRATION_BINARY")
	require.NotEmpty(t, binary, "run scripts/test-integration.sh")
	artifacts := os.Getenv("ADDER_INTEGRATION_ARTIFACTS")
	require.NotEmpty(t, artifacts, "run scripts/test-integration.sh")
	dir, err := os.MkdirTemp(artifacts, strings.ReplaceAll(t.Name(), "/", "-")+"-")
	require.NoError(t, err)
	t.Logf("artifacts: %s", dir)
	mux := http.NewServeMux()
	path, handler := syncconnect.NewSyncServiceHandler(f)
	mux.Handle(path, handler)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(server.Close)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	args := []string{
		"--input", "utxorpc", "--input-utxorpc-url", server.URL,
		"--input-utxorpc-network", "mainnet", "--input-utxorpc-auto-reconnect=false",
		"--api-address", "127.0.0.1", "--api-port", port,
	}
	args = append(args, flags...)
	if config.yaml != "" {
		configPath := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(config.yaml), 0600))
		args = append(args, "--config", configPath)
	}
	p := &process{cmd: exec.Command(binary, args...), done: make(chan struct{}), dir: dir, apiURL: "http://" + address}
	p.cmd.Dir = dir
	// Isolate configuration from the developer's exported plugin settings.
	p.cmd.Env = []string{"GORACE=halt_on_error=1"}
	for _, key := range []string{"HOME", "PATH", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			p.cmd.Env = append(p.cmd.Env, key+"="+value)
		}
	}
	p.cmd.Env = append(p.cmd.Env, config.env...)
	invocation, err := json.MarshalIndent(struct {
		Args []string `json:"args"`
		Env  []string `json:"env"`
	}{args, config.env}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "invocation.json"), invocation, 0600))
	stdout, err := os.Create(filepath.Join(dir, "stdout.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdout.Close() })
	stderr, err := os.Create(filepath.Join(dir, "stderr.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = stderr.Close() })
	p.cmd.Stdout, p.cmd.Stderr = stdout, stderr
	require.NoError(t, p.cmd.Start())
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			<-p.done
		}
		if t.Failed() {
			t.Logf("Adder stderr:\n%s", p.read(t, "stderr.log"))
		}
	})
	return p
}

func (p *process) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.dir, name))
	require.NoError(t, err)
	return string(data)
}

func (p *process) waitOutput(t *testing.T, name string, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(p.dir, name))
		return err == nil && strings.Count(string(data), "\n") == count
	}, 5*time.Second, 10*time.Millisecond, "log output did not receive the expected events")
}

func (p *process) healthy(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get(p.apiURL + "/healthcheck")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		var body map[string]any
		return response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&body) == nil && body["failed"] == false
	}, 10*time.Second, 20*time.Millisecond, "Adder did not become healthy")
}

func (p *process) events(t *testing.T, count int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"/events", nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var types []string
	for len(types) < count && scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt))
		types = append(types, evt.Type)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, types, count)
	// Leave SSE connected through shutdown to catch HTTP shutdown deadlocks.
	return types
}

func (p *process) stop(t *testing.T, signal os.Signal) {
	t.Helper()
	require.NoError(t, p.cmd.Process.Signal(signal))
	select {
	case <-p.done:
		require.NoError(t, p.err, "stderr: %s", p.read(t, "stderr.log"))
	case <-time.After(5 * time.Second):
		t.Fatal("Adder did not shut down within five seconds with SSE connected")
	}
	require.NotContains(t, p.read(t, "stderr.log"), "DATA RACE")
}
