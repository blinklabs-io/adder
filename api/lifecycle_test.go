// Copyright 2025 Blink Labs Software
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

package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/output/embedded"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/stretchr/testify/require"
)

func TestAPIStartReturnsBindError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := uint(occupied.Addr().(*net.TCPAddr).Port)
	mux := ConfigureRouter()
	server := &APIv1{
		mux:     mux,
		handler: withMiddleware(mux),
		Host:    "127.0.0.1",
		Port:    port,
	}

	if err := server.Start(); err == nil {
		t.Fatal("expected occupied port to fail synchronously")
	}
}

func TestAPIShutdownReleasesListener(t *testing.T) {
	mux := ConfigureRouter()
	server := &APIv1{
		mux:     mux,
		handler: withMiddleware(mux),
		Host:    "127.0.0.1",
		Port:    0,
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	address := server.listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("listener still accepted connections after shutdown")
	}
}

// shutdownOrderListener probes the HTTP server's public stopped behavior when
// its listener is closed. An invalid address makes the pre-shutdown probe fail
// immediately instead of opening a second server.
type shutdownOrderListener struct {
	net.Listener
	server   *http.Server
	probeErr error
}

func (l *shutdownOrderListener) Close() error {
	l.probeErr = l.server.ListenAndServe()
	return l.Listener.Close()
}

func TestAPIShutdownMarksServerStoppedBeforeClosingListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Addr: "127.0.0.1:-1"}
	probe := &shutdownOrderListener{Listener: listener, server: server}
	api := &APIv1{server: server, listener: probe}

	// Exercise Shutdown before Serve has registered the listener, which
	// can happen immediately after Start returns.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := api.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(probe.probeErr, http.ErrServerClosed) {
		t.Fatalf(
			"listener closed before HTTP server stopped: %v",
			probe.probeErr,
		)
	}
	conn, err := net.DialTimeout(
		"tcp",
		listener.Addr().String(),
		100*time.Millisecond,
	)
	if err == nil {
		conn.Close()
		t.Fatal("listener not registered with Serve remained open")
	}
}

func TestHealthcheckBecomesUnhealthyOnTerminalPluginFailure(t *testing.T) {
	ResetHealthCheckers()
	t.Cleanup(ResetHealthCheckers)
	p := pipeline.New()
	sink := embedded.New()
	p.AddOutput(sink)
	require.NoError(t, p.Start())
	t.Cleanup(func() { require.NoError(t, p.Stop()) })
	RegisterHealthChecker(p)
	sink.Fail(errors.New("worker stopped"))
	select {
	case <-p.Failed():
	case <-time.After(time.Second):
		t.Fatal("failure not reported")
	}
	response := httptest.NewRecorder()
	handleHealthcheck(
		response,
		httptest.NewRequest(http.MethodGet, "/healthcheck", nil),
	)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
}
