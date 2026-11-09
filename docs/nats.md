# NATS output

The `nats` output publishes selected Adder events as JSON to [Core NATS subjects](https://docs.nats.io/learn/core-nats/publish-subscribe). It uses the [current official Go client](https://github.com/nats-io/nats.go/releases/tag/v1.54.0). Core NATS provides [at-most-once delivery](https://docs.nats.io/learn/core-nats); this output does not require JetStream or persistent storage.

```yaml
output: nats
plugins:
  output:
    nats:
      servers: "nats://127.0.0.1:4222"
      subject-prefix: "adder.events"
      event-types: "blocks,rollbacks,governance"
```

The default subjects are `adder.events.block`, `adder.events.rollback`, and `adder.events.governance`. Optional event types are `transactions`, `drep-registration`, `drep-update`, and `drep-retirement`. Each payload is the complete Adder event JSON. `servers` accepts comma-separated NATS URLs; `token` optionally sets a server authentication token. Supply credentials through secret-managed configuration. Subjects cannot contain spaces or NATS wildcard tokens.

After each publish, the output flushes the connection. A successful flush means the server processed the publish command; it does not prove a subscriber received the event. Failed publishes, flushes, and asynchronous broker rejections are reported on the plugin error channel. There is no retry of an ambiguous flush failure because it could duplicate a message. The client reconnects for later events, with offline publish buffering disabled. Events published while disconnected fail rather than accumulating in a queue. On shutdown the connection closes and pending in-memory events are not recovered.

To check a live deployment, subscribe to `adder.events.>` and send a selected event. Verify the subject and JSON payload, then disconnect the broker and confirm the output reports an error. The package test uses a local Core NATS protocol peer to verify subject mapping and serialized payload without a separate broker.
