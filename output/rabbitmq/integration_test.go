// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0
package rabbitmq

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

// Set ADDER_RABBITMQ_TEST_URL to run against a disposable local broker.
func TestRabbitMQBrokerContract(t *testing.T) {
	broker := os.Getenv("ADDER_RABBITMQ_TEST_URL")
	if broker == "" {
		t.Skip("set ADDER_RABBITMQ_TEST_URL for a disposable broker")
	}
	conn, err := amqp.Dial(broker)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()
	queue, err := ch.QueueDeclare("", true, false, false, false, nil)
	require.NoError(t, err)
	defer ch.QueueDelete(queue.Name, false, false, false)
	r := New(WithURL(broker), WithRoutingKey(queue.Name))
	require.NoError(t, r.connect(context.Background()))
	defer r.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	evt := event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	require.NoError(t, r.publish(ctx, evt))
	delivery, ok, err := ch.Get(queue.Name, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, amqp.Persistent, delivery.DeliveryMode)
	require.Contains(t, string(delivery.Body), "input.rollback")
	r.routingKey = "adder-unbound-" + time.Now().Format("20060102150405.000000000")
	require.ErrorContains(t, r.publish(ctx, evt), "unroutable")
}
