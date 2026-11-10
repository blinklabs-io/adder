// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0
package rabbitmq

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/plugin"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

func TestPublisherConfirmContract(t *testing.T) {
	done := make(chan struct{})
	close(done)
	for _, tc := range []struct {
		name     string
		acked    bool
		returned bool
		want     string
	}{
		{"ack", true, false, ""}, {"nack", false, false, "nack"}, {"returned-and-ack", true, true, "unroutable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			returns := make(chan amqp.Return, 1)
			if tc.returned {
				returns <- amqp.Return{ReplyText: "NO_ROUTE"}
			}
			err := awaitConfirm(context.Background(), done, func() bool { return tc.acked }, returns, time.Second)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestPublisherConfirmTimeoutAndCancellation(t *testing.T) {
	returns := make(chan amqp.Return)
	done := make(chan struct{})
	require.ErrorContains(t, awaitConfirm(context.Background(), done, func() bool { return false }, returns, time.Millisecond), "ambiguous")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, awaitConfirm(ctx, done, func() bool { return false }, returns, time.Second), context.Canceled)
}

func TestRabbitMQURL(t *testing.T) {
	for _, raw := range []string{"http://localhost", "amqp:///", "amqps://host/#fragment", "amqp://127.0.0.1:99999/"} {
		require.Error(t, validateURL(raw))
	}
	require.NoError(t, validateURL("amqps://host/vhost"))
}
func TestPersistentBoolConfiguration(t *testing.T) {
	p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "rabbitmq", map[string]any{"persistent": false})
	require.NoError(t, err)
	require.False(t, p.(*RabbitMQOutput).persistent)
}

func TestBoundedWriteAndStop(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	start := time.Now()
	_, err := (boundedConn{left, 20 * time.Millisecond}).Write([]byte("publish"))
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second)
	r := New()
	r.transport = left
	require.NoError(t, r.close())
}
