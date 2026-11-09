# Matrix output

The Matrix output sends selected Adder events as plain-text `m.room.message` events to one or more rooms. It uses the [Matrix Client-Server API v1.19 room send endpoint](https://spec.matrix.org/v1.19/client-server-api/#put_matrixclientv3roomsroomidsendeventtypetxnid) with an access token in the `Authorization: Bearer` header. The request body is `{"msgtype":"m.text","body":"..."}`. Each message gets a unique transaction ID; retries reuse that ID so the homeserver can deduplicate them.

Configure the output in YAML:

```yaml
output: matrix
plugins:
  output:
    matrix:
      homeserver-url: https://matrix.example.org
      access-token: your-bot-access-token
      room-ids: '!alerts:example.org,!ops:example.org'
      event-types: blocks,rollbacks,governance
```

The access token belongs to a Matrix account that has joined each destination room and can send messages there. Keep the token in a secret-managed configuration. `homeserver-url` must use HTTPS; HTTP is accepted only for loopback testing. `room-ids` accepts comma-separated room IDs. The default event selection is `blocks,rollbacks,governance`; optional types are `transactions`, `drep-registration`, `drep-update`, and `drep-retirement`.

Messages contain the event type and Adder event JSON. Delivery is best effort. The output retries network errors and server errors up to three times, and observes the Matrix [rate-limit response](https://spec.matrix.org/v1.19/client-server-api/#rate-limiting) `Retry-After` header (or legacy `retry_after_ms` field) for waits of at most one minute. When neither hint is supplied, it uses bounded backoff. Permanent client errors are reported without retry. Delivery errors go to the plugin error channel. There is no persistent queue; messages in flight can be lost when Adder stops. A successful room-send response confirms acceptance by the homeserver, not that a user read the message.

To check a live deployment, send one selected event, verify its room message, then test an unauthorized token and a room the account has not joined. The package tests use a local HTTP server to verify the published request and response contract without requiring an external account.
