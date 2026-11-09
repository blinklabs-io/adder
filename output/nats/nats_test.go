// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package nats

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

type published struct {
	subject string
	payload []byte
}

type blockingLogger struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*blockingLogger) Info(string, ...any)    {}
func (*blockingLogger) Warn(string, ...any)    {}
func (*blockingLogger) Debug(string, ...any)   {}
func (*blockingLogger) Fatal(string, ...any)   {}
func (l *blockingLogger) Error(string, ...any) { l.once.Do(func() { close(l.entered) }); <-l.release }

// A minimal Core NATS peer checks the published protocol without requiring an
// installed server. The production client is the current official nats.go.
func fakeServer(t *testing.T) (string, <-chan published) {
	return fakeServerWithReject(t, false)
}

func fakeServerWithReject(t *testing.T, reject bool) (string, <-chan published) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan published, 2)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "INFO {\"server_id\":\"test\",\"version\":\"2.10.0\",\"proto\":1,\"max_payload\":1048576}\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			switch {
			case line == "PING":
				_, _ = io.WriteString(conn, "PONG\r\n")
			case strings.HasPrefix(line, "PUB "):
				var subject string
				var size int
				if _, err := fmt.Sscanf(line, "PUB %s %d", &subject, &size); err != nil {
					t.Error(err)
					return
				}
				payload := make([]byte, size+2)
				if _, err := io.ReadFull(reader, payload); err != nil {
					t.Error(err)
					return
				}
				if string(payload[size:]) != "\r\n" {
					t.Error("invalid PUB terminator")
				}
				messages <- published{subject, payload[:size]}
				if reject {
					_, _ = io.WriteString(conn, "-ERR 'Permissions Violation for Publish to \"cardano.adder.rollback\"'\r\n")
				}
			}
		}
	}()
	return "nats://" + listener.Addr().String(), messages
}

func TestNATSPermissionRejectionIsReported(t *testing.T) {
	server, _ := fakeServerWithReject(t, true)
	n := New(WithServers(server), WithSubjectPrefix("cardano.adder"))
	require.NoError(t, n.Start())
	n.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case err := <-n.ErrorChan():
		require.ErrorContains(t, err, "Permissions Violation")
	case <-time.After(3 * time.Second):
		t.Fatal("NATS permission rejection was not reported")
	}
	require.NoError(t, n.Stop())
}

func TestNATSStopWaitsForAsyncCallback(t *testing.T) {
	server, _ := fakeServerWithReject(t, true)
	logger := &blockingLogger{entered: make(chan struct{}), release: make(chan struct{})}
	n := New(WithServers(server), WithSubjectPrefix("cardano.adder"), WithLogger(logger))
	require.NoError(t, n.Start())
	n.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case <-logger.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("async callback did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- n.Stop() }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before callback completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(logger.release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not join callback")
	}
}

func TestNATSFailedRestartDoesNotWaitForOldConnection(t *testing.T) {
	server, _ := fakeServer(t)
	n := New(WithServers(server))
	require.NoError(t, n.Start())
	require.NoError(t, n.Stop())
	n.servers = []string{"nats://127.0.0.1:1"}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- n.StartContext(ctx) }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("failed restart waited on old connection")
	}
}

func TestPublishCoreNATSEvent(t *testing.T) {
	server, messages := fakeServer(t)
	n := New(WithServers(server), WithSubjectPrefix("cardano.adder"))
	require.NoError(t, n.Start())
	n.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"hash": "ignored"})
	n.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case msg := <-messages:
		require.Equal(t, "cardano.adder.rollback", msg.subject)
		var got event.Event
		require.NoError(t, json.Unmarshal(msg.payload, &got))
		require.Equal(t, event.TypeRollback, got.Type)
	case <-time.After(3 * time.Second):
		t.Fatal("no NATS publication")
	}
	require.NoError(t, n.Stop())
}

func TestNATSFactoryRejectsInvalidConfig(t *testing.T) {
	for _, tt := range []struct {
		values map[string]any
		match  string
	}{
		{map[string]any{"servers": "https://example.org"}, "servers"},
		{map[string]any{"servers": "nats://secret:password@example.org"}, "servers"},
		{map[string]any{"subject-prefix": "bad.*"}, "subject-prefix"},
		{map[string]any{"event-types": "unknown"}, "event type"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "nats", tt.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tt.match)
		require.NotContains(t, err.Error(), "password")
	}
}
