# Amazon SQS and SNS outputs

`sqs` sends selected Adder events to one queue. `sns` publishes selected events
to one topic. Both use the [AWS SDK for Go v2](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html)
and its default credential chain. Supply credentials through an IAM role,
profile, or environment; no access keys are stored in Adder configuration.

```yaml
output: sqs
plugins:
  output:
    sqs:
      region: us-east-1
      queue-url: https://sqs.us-east-1.amazonaws.com/123456789012/adder.fifo
      group-id: chain
      event-types: blocks,rollbacks,governance
```

```yaml
output: sns
plugins:
  output:
    sns:
      region: us-east-1
      topic-arn: arn:aws:sns:us-east-1:123456789012:adder.fifo
      group-id: chain
      max-message-bytes: 262144
      event-types: blocks,rollbacks,governance
```

The event JSON is the `MessageBody` in [SQS SendMessage](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html)
or the `Message` in [SNS Publish](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html).
Each event is sent in its own API call; this output does not batch or keep a
durable local queue. The SDK handles its normal transient retries within a
ten-second request deadline. Failed requests are reported through the output
error channel and are not replayed. An API success means SQS accepted a
message into the queue or SNS accepted a publication for fanout; it does not
prove that a consumer received it. Rollbacks are published as rollback event
records, leaving interpretation of earlier events to consumers.

FIFO destinations require `group-id`. The output supplies a SHA-256 digest of
the complete event JSON as `MessageDeduplicationId`; identical event JSON can
be deduplicated within the service's deduplication window. Only retries of
the same event JSON deduplicate; an event re-emitted with a new timestamp has
a different digest. Standard destinations may omit `group-id`. Setting it on a
[standard SQS queue](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html)
enables fair queues. On a
[standard SNS topic](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html),
SNS forwards it only to standard SQS subscriptions for fair queues. It does not
provide FIFO ordering or deduplication on standard destinations. `transactions`,
`drep-registration`,
`drep-update`, and `drep-retirement` are additional selectable event types.

SQS messages are limited to 1 MiB. SNS topics default to 256 KiB. The
[current SNS API](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)
allows a topic's `MaximumMessageSize` to be configured up to 1 MiB; set
`max-message-bytes` to the same value when using that feature. The service
may enforce a smaller destination limit and return an error.
