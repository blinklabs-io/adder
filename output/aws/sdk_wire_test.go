// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/blinklabs-io/adder/event"
	"github.com/stretchr/testify/require"
)

type wireRequest struct {
	header http.Header
	body   string
}

func TestOfficialSDKSignsAndSerializesRequests(t *testing.T) {
	requests := make(chan wireRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- wireRequest{header: r.Header.Clone(), body: string(body)}
		if !strings.Contains(r.Header.Get("X-Amz-Target"), "SendMessage") {
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(`<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/"><PublishResult><MessageId>accepted</MessageId></PublishResult><ResponseMetadata><RequestId>test</RequestId></ResponseMetadata></PublishResponse>`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MessageId":"accepted"}`))
	}))
	t.Cleanup(server.Close)
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("test-key", "test-secret", "")),
		HTTPClient:  server.Client(),
	}
	evt := event.New(event.TypeRollback, time.Now(), nil, event.RollbackEvent{SlotNumber: 42})
	q := NewSQS(WithRegion("us-east-1"), WithQueueURL(server.URL+"/123/queue"), WithGroupID("tenant"))
	q.sqsClient = sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(server.URL) })
	require.NoError(t, q.publish(context.Background(), evt))
	s := NewSNS(WithRegion("us-east-1"), WithTopicARN("arn:aws:sns:us-east-1:123:topic"), WithGroupID("tenant"))
	s.snsClient = sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(server.URL) })
	require.NoError(t, s.publish(context.Background(), evt))
	sqsRequest := <-requests
	require.Contains(t, sqsRequest.header.Get("Authorization"), "AWS4-HMAC-SHA256")
	require.Equal(t, "AmazonSQS.SendMessage", sqsRequest.header.Get("X-Amz-Target"))
	var sqsBody struct {
		QueueUrl               string
		MessageBody            string
		MessageGroupId         string
		MessageDeduplicationId *string
	}
	require.NoError(t, json.Unmarshal([]byte(sqsRequest.body), &sqsBody))
	require.Equal(t, server.URL+"/123/queue", sqsBody.QueueUrl)
	require.Contains(t, sqsBody.MessageBody, "rollback")
	require.Equal(t, "tenant", sqsBody.MessageGroupId)
	require.Nil(t, sqsBody.MessageDeduplicationId)
	snsRequest := <-requests
	require.Contains(t, snsRequest.header.Get("Authorization"), "AWS4-HMAC-SHA256")
	form, err := url.ParseQuery(snsRequest.body)
	require.NoError(t, err)
	require.Equal(t, "Publish", form.Get("Action"))
	require.Equal(t, "arn:aws:sns:us-east-1:123:topic", form.Get("TopicArn"))
	require.Contains(t, form.Get("Message"), "rollback")
	require.Equal(t, "tenant", form.Get("MessageGroupId"))
	require.NotContains(t, form, "MessageDeduplicationId")
}
