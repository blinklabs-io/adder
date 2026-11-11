# Redis Streams output

The `redis` output appends selected Adder events to a Redis Stream using
[`XADD`](https://redis.io/docs/latest/commands/xadd/) and the
[go-redis v9 client](https://github.com/redis/go-redis/releases/tag/v9.23.0).
`XADD` is available in Redis Open Source 5.0 and later. The optional ACL
`username` requires Redis 6.0 or later (see [AUTH](https://redis.io/docs/latest/commands/auth/)), and this output does not require
Redis 8.6 idempotent producer extensions.

```yaml
output: redis
plugins:
  output:
    redis:
      address: "127.0.0.1:6379"
      stream: "adder.events"
      event-types: "blocks,rollbacks,governance"
```

Set `username` and `password` for Redis ACL authentication, or `password`
alone for the default user. Credentials require `tls: true`; configure the
server for [Redis TLS](https://redis.io/docs/latest/operate/oss_and_stack/management/security/encryption/).
The default event types are
`blocks,rollbacks,governance`; optional types are `transactions`,
`drep-registration`, `drep-update`, and `drep-retirement`.

Each selected event produces one `XADD` entry in the configured stream. Redis
generates the entry ID. The `type` field is the Adder event type, and `event`
is the complete event JSON. A successful Redis reply confirms that Redis
accepted the append; it does not confirm persistence to disk or consumption.
Consumers can inspect entries with [`XRANGE`](https://redis.io/docs/latest/commands/xrange/)
or read with [`XREAD`](https://redis.io/docs/latest/commands/xread/).

Each append has a five-second deadline. The client does not retry commands;
an error is sent to the plugin error channel. A connection failure before
Redis receives the command means the event was not stored, while a lost reply
leaves delivery uncertain. Adder does not replay either case automatically,
because repeating `XADD *` after a lost reply can create a duplicate. Later
events use the client connection pool and may reconnect. Pending events are
not recovered after shutdown or restart. Size and retention of the stream are
managed by the Redis operator; this output does not trim entries.

For a live check, start Redis, publish a selected Adder event, and run
`XRANGE adder.events - +` to check the ID, `type`, and JSON `event` fields.
Then stop Redis and confirm Adder reports an output error. The package test
checks the same `XADD` and `XRANGE` behavior with an in-process Redis server.
