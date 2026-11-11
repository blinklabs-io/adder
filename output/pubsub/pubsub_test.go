// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	gcp "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type localPublisher struct {
	pubsubpb.UnimplementedPublisherServer
	requests  chan *pubsubpb.PublishRequest
	block     bool
	cancelled chan struct{}
}

func (p *localPublisher) Publish(ctx context.Context, req *pubsubpb.PublishRequest) (*pubsubpb.PublishResponse, error) {
	p.requests <- req
	if p.block {
		<-ctx.Done()
		close(p.cancelled)
		return nil, ctx.Err()
	}
	ids := make([]string, len(req.Messages))
	for i := range ids {
		ids[i] = "test-message-id"
	}
	return &pubsubpb.PublishResponse{MessageIds: ids}, nil
}

func TestStopBoundsOutstandingSDKPublish(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	peer := &localPublisher{requests: make(chan *pubsubpb.PublishRequest, 1), block: true, cancelled: make(chan struct{})}
	pubsubpb.RegisterPublisherServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("PUBSUB_EMULATOR_HOST", listener.Addr().String())
	o := New(WithProjectID("test-project"), WithTopicID("adder-events"))
	o.timeout = 200 * time.Millisecond
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case <-peer.requests:
	case <-time.After(time.Second):
		t.Fatal("publish RPC did not start")
	}
	done := make(chan error, 1)
	go func() { done <- o.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited past publisher timeout")
	}
	select {
	case <-peer.cancelled:
	case <-time.After(time.Second):
		t.Fatal("publisher RPC was not cancelled")
	}
}

func TestOfficialClientPublishesToLocalProtocolServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	peer := &localPublisher{requests: make(chan *pubsubpb.PublishRequest, 1)}
	pubsubpb.RegisterPublisherServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("PUBSUB_EMULATOR_HOST", listener.Addr().String())
	o := New(WithProjectID("test-project"), WithTopicID("adder-events"))
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	evt := event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	require.NoError(t, o.publish(context.Background(), evt))
	select {
	case req := <-peer.requests:
		require.Equal(t, "projects/test-project/topics/adder-events", req.Topic)
		require.Len(t, req.Messages, 1)
		require.Equal(t, event.TypeRollback, req.Messages[0].Attributes["event_type"])
		var got event.Event
		require.NoError(t, json.Unmarshal(req.Messages[0].Data, &got))
		require.Equal(t, event.TypeRollback, got.Type)
	case <-time.After(time.Second):
		t.Fatal("no Pub/Sub request")
	}
}

func TestSelectionAndFailedPublish(t *testing.T) {
	o := New(WithProjectID("test-project"), WithTopicID("adder-events"))
	calls := make(chan *gcp.Message, 1)
	o.publishFn = func(_ context.Context, msg *gcp.Message) (string, error) {
		calls <- msg
		return "", errors.New("unavailable")
	}
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"hash": "filtered"})
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "unavailable")
	case <-time.After(time.Second):
		t.Fatal("failed publish not reported")
	}
	require.Len(t, calls, 1)
}

func TestStopCancelsInFlightPublish(t *testing.T) {
	o := New(WithProjectID("test-project"), WithTopicID("adder-events"))
	started := make(chan struct{})
	o.publishFn = func(ctx context.Context, _ *gcp.Message) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("publish did not start")
	}
	done := make(chan error, 1)
	go func() { done <- o.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel publish")
	}
}

func TestFactoryRejectsInvalidNames(t *testing.T) {
	for _, tc := range []struct {
		values map[string]any
		match  string
	}{
		{map[string]any{"project-id": "bad/project", "topic-id": "adder-events"}, "project-id"},
		{map[string]any{"project-id": "Bad!Project", "topic-id": "adder-events"}, "project-id"},
		{map[string]any{"project-id": "bad-", "topic-id": "adder-events"}, "project-id"},
		{map[string]any{"project-id": "project", "topic-id": "1bad"}, "topic-id"},
		{map[string]any{"project-id": "project", "topic-id": "goog-events"}, "topic-id"},
		{map[string]any{"project-id": "project", "topic-id": strings.Repeat("a", 256)}, "topic-id"},
		{map[string]any{"project-id": "project", "topic-id": "adder-events", "event-types": "unknown"}, "event type"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "pubsub", tc.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tc.match)
	}
}

func TestPubSubProjectIdentifiers(t *testing.T) {
	for _, id := range []string{"test-project", "123456789123", "example.com:my-project"} {
		require.True(t, validProjectID(id), id)
	}
	for _, id := range []string{"Bad!Project", "bad/project", "-bad-project", "bad-project-", "a:b:c"} {
		require.False(t, validProjectID(id), id)
	}
}

func TestMessageLimitUsesEncodedBytes(t *testing.T) {
	o := New(WithProjectID("test-project"), WithTopicID("adder-events"))
	called := false
	o.publishFn = func(context.Context, *gcp.Message) (string, error) { called = true; return "id", nil }
	evt := event.New(event.TypeRollback, time.Now(), nil, strings.Repeat("é", maxMessageBytes/2))
	require.ErrorContains(t, o.publish(context.Background(), evt), "10 MB")
	require.False(t, called)
}
