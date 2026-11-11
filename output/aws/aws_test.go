// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

const (
	queueURL = "https://sqs.us-east-1.amazonaws.com/123456789012/adder.fifo"
	topicARN = "arn:aws:sns:us-east-1:123456789012:adder.fifo"
)

type fakeSQS struct {
	calls chan *sqs.SendMessageInput
	err   error
}

func (f *fakeSQS) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.calls <- in
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.SendMessageOutput{MessageId: aws.String("accepted")}, nil
}

type fakeSNS struct {
	calls chan *sns.PublishInput
	err   error
}

type blockingSQS struct{ started chan struct{} }

func (f *blockingSQS) SendMessage(ctx context.Context, _ *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeSNS) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	f.calls <- in
	if f.err != nil {
		return nil, f.err
	}
	return &sns.PublishOutput{MessageId: aws.String("accepted")}, nil
}

func eventFromJSON(t *testing.T, raw string) event.Event {
	t.Helper()
	var evt event.Event
	require.NoError(t, json.Unmarshal([]byte(raw), &evt))
	return evt
}

func TestSQSUsesDocumentedSendMessageFields(t *testing.T) {
	fake := &fakeSQS{calls: make(chan *sqs.SendMessageInput, 2)}
	o := NewSQS(WithRegion("us-east-1"), WithQueueURL(queueURL), WithGroupID("chain"))
	o.sqsClient = fake
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeTransaction, time.Now(), nil, map[string]any{"hash": "filtered"})
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case in := <-fake.calls:
		require.Equal(t, queueURL, aws.ToString(in.QueueUrl))
		require.Equal(t, "chain", aws.ToString(in.MessageGroupId))
		require.Equal(t, event.TypeRollback, eventFromJSON(t, aws.ToString(in.MessageBody)).Type)
		sum := sha256.Sum256([]byte(aws.ToString(in.MessageBody)))
		require.Equal(t, hex.EncodeToString(sum[:]), aws.ToString(in.MessageDeduplicationId))
	case <-time.After(time.Second):
		t.Fatal("SQS request not sent")
	}
}

func TestSNSUsesDocumentedPublishFields(t *testing.T) {
	fake := &fakeSNS{calls: make(chan *sns.PublishInput, 1)}
	o := NewSNS(WithRegion("us-east-1"), WithTopicARN(topicARN), WithGroupID("chain"))
	o.snsClient = fake
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case in := <-fake.calls:
		require.Equal(t, topicARN, aws.ToString(in.TopicArn))
		require.Equal(t, "chain", aws.ToString(in.MessageGroupId))
		require.NotEmpty(t, aws.ToString(in.MessageDeduplicationId))
		require.Equal(t, event.TypeRollback, eventFromJSON(t, aws.ToString(in.Message)).Type)
	case <-time.After(time.Second):
		t.Fatal("SNS request not sent")
	}
}

func TestAWSFailuresAreReported(t *testing.T) {
	fake := &fakeSQS{calls: make(chan *sqs.SendMessageInput, 1), err: errors.New("service unavailable")}
	o := NewSQS(WithRegion("us-east-1"), WithQueueURL(queueURL), WithGroupID("chain"))
	o.sqsClient = fake
	require.NoError(t, o.Start())
	t.Cleanup(func() { require.NoError(t, o.Stop()) })
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case err := <-o.ErrorChan():
		require.ErrorContains(t, err, "service unavailable")
	case <-time.After(time.Second):
		t.Fatal("SQS failure not reported")
	}
}

func TestFactoryRejectsInvalidDestinations(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		values map[string]any
		match  string
	}{
		{"sqs", map[string]any{"region": "us-east-1", "queue-url": "http://example.org/queue"}, "queue-url"},
		{"sqs", map[string]any{"region": "us-west-2", "queue-url": queueURL, "group-id": "chain"}, "region"},
		{"sqs", map[string]any{"region": "us-east-1", "queue-url": queueURL}, "group-id"},
		{"sns", map[string]any{"region": "us-east-1", "topic-arn": "arn:aws:sqs:us-east-1:123:bad"}, "topic-arn"},
		{"sns", map[string]any{"region": "us-east-1", "topic-arn": topicARN}, "group-id"},
		{"sqs", map[string]any{"region": "us-east-1", "queue-url": queueURL, "group-id": "chain", "event-types": "unknown"}, "event type"},
		{"sqs", map[string]any{"region": "us-east-1", "queue-url": queueURL, "group-id": "bad space"}, "group-id"},
		{"sns", map[string]any{"region": "us-east-1", "topic-arn": topicARN, "group-id": strings.Repeat("a", 129)}, "group-id"},
		{"sns", map[string]any{"region": "us-east-1", "topic-arn": topicARN, "group-id": "é"}, "group-id"},
		{"sns", map[string]any{"region": "us-east-1", "topic-arn": topicARN, "group-id": "chain", "max-message-bytes": 1048577}, "max-message-bytes"},
	} {
		p, err := plugin.GetPlugin(plugin.PluginTypeOutput, tc.kind, tc.values)
		require.Nil(t, p)
		require.ErrorContains(t, err, tc.match)
	}
}

func sizedEvent(t *testing.T, bytes int) event.Event {
	t.Helper()
	evt := event.New(event.TypeRollback, time.Now(), nil, "")
	base, err := json.Marshal(evt)
	require.NoError(t, err)
	require.GreaterOrEqual(t, bytes, len(base))
	evt.Payload = strings.Repeat("x", bytes-len(base))
	encoded, err := json.Marshal(evt)
	require.NoError(t, err)
	require.Len(t, encoded, bytes)
	return evt
}

func TestMessageSizeLimitsUseEncodedBytes(t *testing.T) {
	sqsFake := &fakeSQS{calls: make(chan *sqs.SendMessageInput, 2)}
	q := NewSQS(WithRegion("us-east-1"), WithQueueURL(queueURL), WithGroupID("chain"))
	q.sqsClient = sqsFake
	require.NoError(t, q.Start())
	t.Cleanup(func() { require.NoError(t, q.Stop()) })
	require.NoError(t, q.publish(context.Background(), sizedEvent(t, 1<<20)))
	require.ErrorContains(t, q.publish(context.Background(), sizedEvent(t, 1<<20+1)), "1 MiB")
	require.Len(t, sqsFake.calls, 1)

	snsFake := &fakeSNS{calls: make(chan *sns.PublishInput, 2)}
	s := NewSNS(WithRegion("us-east-1"), WithTopicARN(topicARN), WithGroupID("chain"), WithMaxMessageBytes(1024))
	s.snsClient = snsFake
	require.NoError(t, s.Start())
	t.Cleanup(func() { require.NoError(t, s.Stop()) })
	require.NoError(t, s.publish(context.Background(), sizedEvent(t, 1024)))
	require.ErrorContains(t, s.publish(context.Background(), sizedEvent(t, 1025)), "size limit")
	require.Len(t, snsFake.calls, 1)
	unicodeEvent := event.New(event.TypeRollback, time.Now(), nil, strings.Repeat("é", 500))
	require.ErrorContains(t, s.publish(context.Background(), unicodeEvent), "size limit")
	require.Len(t, snsFake.calls, 1)
}

func TestStandardDestinationsOmitDeduplication(t *testing.T) {
	for _, tc := range []struct {
		name, groupID string
	}{
		{"without group ID", ""},
		{"with group ID", "tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})

			sqsFake := &fakeSQS{calls: make(chan *sqs.SendMessageInput, 1)}
			q := NewSQS(WithRegion("us-east-1"), WithQueueURL(strings.TrimSuffix(queueURL, ".fifo")), WithGroupID(tc.groupID))
			q.sqsClient = sqsFake
			require.NoError(t, q.Start())
			t.Cleanup(func() { require.NoError(t, q.Stop()) })
			require.NoError(t, q.publish(context.Background(), evt))
			sqsInput := <-sqsFake.calls
			require.Equal(t, tc.groupID, aws.ToString(sqsInput.MessageGroupId))
			require.Nil(t, sqsInput.MessageDeduplicationId)

			snsFake := &fakeSNS{calls: make(chan *sns.PublishInput, 1)}
			s := NewSNS(WithRegion("us-east-1"), WithTopicARN(strings.TrimSuffix(topicARN, ".fifo")), WithGroupID(tc.groupID))
			s.snsClient = snsFake
			require.NoError(t, s.Start())
			t.Cleanup(func() { require.NoError(t, s.Stop()) })
			require.NoError(t, s.publish(context.Background(), evt))
			snsInput := <-snsFake.calls
			require.Equal(t, tc.groupID, aws.ToString(snsInput.MessageGroupId))
			require.Nil(t, snsInput.MessageDeduplicationId)
		})
	}
}

func TestStopCancelsInFlightSend(t *testing.T) {
	fake := &blockingSQS{started: make(chan struct{})}
	o := NewSQS(WithRegion("us-east-1"), WithQueueURL(queueURL), WithGroupID("chain"))
	o.sqsClient = fake
	require.NoError(t, o.Start())
	o.InputChan() <- event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	done := make(chan error, 1)
	go func() { done <- o.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel request")
	}
}
