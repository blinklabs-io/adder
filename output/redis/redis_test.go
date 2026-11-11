// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	redisgo "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestXAddStoresSelectedJSONWithServerID(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)
	o := New(WithAddress(server.Addr()), WithStream("cardano.events"))
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"ignored": true})
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	client := redisgo.NewClient(&redisgo.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.Eventually(t, func() bool {
		return client.XLen(context.Background(), "cardano.events").Val() == 1
	}, time.Second, time.Millisecond)
	entries, err := client.XRange(context.Background(), "cardano.events", "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Regexp(t, `^[0-9]+-[0-9]+$`, entries[0].ID)
	require.Equal(t, event.TypeRollback, entries[0].Values["type"])
	var got event.Event
	require.NoError(t, json.Unmarshal([]byte(entries[0].Values["event"].(string)), &got))
	require.Equal(t, event.TypeRollback, got.Type)
}

func TestRedisErrorIsReportedWithoutRetry(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)
	o := New(WithAddress(server.Addr()))
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	server.SetError("temporary outage")
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "temporary outage")
		require.ErrorContains(t, err, "delivery uncertain")
	case <-time.After(time.Second):
		t.Fatal("Redis failure was not reported")
	}
	server.SetError("")
	time.Sleep(20 * time.Millisecond)
	require.False(t, server.Exists("adder.events"), "failed event must not be queued for replay")
}

func TestFactoryRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		values  map[string]any
		message string
	}{
		{map[string]any{"address": "redis.example.org"}, "address"},
		{map[string]any{"address": "redis.example.org:0"}, "address"},
		{map[string]any{"stream": ""}, "stream"},
		{map[string]any{"event-types": "unknown"}, "event type"},
		{map[string]any{"event-types": ""}, "event-types"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "redis", tc.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tc.message)
	}
}

func TestStartReportsConnectionFailure(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	addr := server.Addr()
	server.Close()
	o := New(WithAddress(addr))
	o.timeout = 50 * time.Millisecond
	require.ErrorContains(t, o.Start(), "connect Redis")
}
