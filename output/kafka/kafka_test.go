// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kmsg"
)

type producedRecord struct {
	topicID   [16]byte
	acks      int16
	partition int32
	key       string
	value     []byte
	err       error
}

func TestProduceUsesDocumentedTopicKeyAndAllISRAcks(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.SeedTopics(3, "cardano-events"))
	require.NoError(t, err)
	t.Cleanup(cluster.Close)
	produced := make(chan producedRecord, 2)
	cluster.ControlKey(int16(kmsg.Produce), func(request kmsg.Request) (kmsg.Response, error, bool) {
		req := request.(*kmsg.ProduceRequest)
		got := producedRecord{acks: req.Acks}
		if len(req.Topics) != 1 || len(req.Topics[0].Partitions) != 1 {
			got.err = fmt.Errorf("expected one topic and partition")
		} else {
			got.topicID = req.Topics[0].TopicID
			got.partition = req.Topics[0].Partitions[0].Partition
			batch := kmsg.NewRecordBatch()
			got.err = batch.ReadFrom(req.Topics[0].Partitions[0].Records)
			if got.err == nil {
				record := kmsg.NewRecord()
				got.err = record.ReadFrom(batch.Records)
				got.key = string(record.Key)
				got.value = record.Value
			}
		}
		produced <- got
		return nil, nil, false
	})
	o := New(WithBrokers(cluster.ListenAddrs()...), WithTopic("cardano-events"))
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"ignored": true})
	o.InputChan() <- event.New(event.TypeBlock, time.Now(), nil, map[string]any{"block": 1})
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	var firstPartition int32
	for i, expectedType := range []string{event.TypeBlock, event.TypeRollback} {
		select {
		case got := <-produced:
			require.NoError(t, got.err)
			require.Equal(t, cluster.TopicInfo("cardano-events").TopicID, got.topicID)
			require.Equal(t, int16(-1), got.acks)
			require.Equal(t, "adder.events", got.key)
			if i == 0 {
				firstPartition = got.partition
			} else {
				require.Equal(t, firstPartition, got.partition)
			}
			var evt event.Event
			require.NoError(t, json.Unmarshal(got.value, &evt))
			require.Equal(t, expectedType, evt.Type)
		case <-time.After(3 * time.Second):
			t.Fatal("no Kafka produce request")
		}
	}
	select {
	case got := <-produced:
		t.Fatalf("unexpected second produce: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestProducerReportsDisconnectedBroker(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.SeedTopics(1, "adder.events"))
	require.NoError(t, err)
	o := New(WithBrokers(cluster.ListenAddrs()...))
	o.timeout = time.Second
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	cluster.Close()
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "delivery uncertain")
	case <-time.After(2 * time.Second):
		t.Fatal("broker failure was not reported")
	}
}

func TestStopCancelsInFlightProduce(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.SeedTopics(1, "adder.events"))
	require.NoError(t, err)
	t.Cleanup(cluster.Close)
	o := New(WithBrokers(cluster.ListenAddrs()...))
	o.timeout = time.Second
	require.NoError(t, o.Start())
	entered := make(chan struct{}, 1)
	cluster.ControlKey(int16(kmsg.Produce), func(kmsg.Request) (kmsg.Response, error, bool) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-o.Done()
		return nil, context.Canceled, true
	})
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Kafka produce request did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- o.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Kafka output did not stop")
	}
}

func TestFactoryRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		values  map[string]any
		message string
	}{
		{map[string]any{"brokers": ""}, "brokers"},
		{map[string]any{"brokers": "broker"}, "brokers"},
		{map[string]any{"topic": "bad/topic"}, "topic"},
		{map[string]any{"topic": ""}, "topic"},
		{map[string]any{"username": "user"}, "password"},
		{map[string]any{"username": "user", "password": "secret"}, "tls"},
		{map[string]any{"event-types": "unknown"}, "event type"},
		{map[string]any{"event-types": ""}, "event-types"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "kafka", tc.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tc.message)
		if err != nil {
			require.NotContains(t, err.Error(), "secret")
		}
	}
}
