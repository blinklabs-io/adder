# Google Cloud Pub/Sub output

The `pubsub` output sends selected Adder events to one existing Pub/Sub topic
using the [official Go Pub/Sub v2 client](https://pkg.go.dev/cloud.google.com/go/pubsub/v2@v2.9.0).
It uses [Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials).

```yaml
output: pubsub
plugins:
  output:
    pubsub:
      project-id: my-project
      topic-id: adder-events
      event-types: blocks,rollbacks,governance
```

The topic is `projects/{project-id}/topics/{topic-id}`. The output sends the
complete Adder event JSON as message `Data` and sets the `event_type` attribute.
`transactions`, `drep-registration`, `drep-update`, and `drep-retirement` are
additional selectable types. Rollbacks remain separate event records; consumers
decide how they affect prior events.

The output waits for [PublishResult.Get](https://docs.cloud.google.com/go/docs/reference/cloud.google.com/go/pubsub/v2/latest#Publisher.Publish)
before accepting the next event. This returns after the service accepts or
rejects the message; it does not acknowledge subscriber processing. The
publisher's count threshold is one, so events are sent without intentional
batching. A request has a ten-second deadline. Failures are reported through
the output error channel, with no local durable replay. Shutdown stops
accepting events and waits for the publisher's outstanding attempt to finish
within its ten-second timeout before closing the client. A publication can
still succeed after the output has stopped waiting for its result.

The [current Pub/Sub limits](https://cloud.google.com/pubsub/quotas) cap a
message's data field at 10 MB and a publish request at 10 MB. The output checks
the data limit; the service may reject a message near the limit once request
overhead is included. The topic ID follows the [documented naming rules](https://cloud.google.com/pubsub/docs/create-topic).
