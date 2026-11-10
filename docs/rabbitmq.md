# RabbitMQ output

The `rabbitmq` output publishes selected Adder events as JSON through the
[official Go AMQP 0-9-1 client](https://github.com/rabbitmq/amqp091-go/releases/tag/v1.15.0).
Configure `plugins.output.rabbitmq.url`, `exchange`, and `routing-key`. The
default exchange (`exchange: ""`) routes by queue name; the target queue must
already exist. A named exchange and its bindings must also exist. The plugin
does not create broker topology. Use `amqps://` when connecting outside a
trusted network.

The default event selection is `blocks,rollbacks,governance`; optional types
are `transactions`, `drep-registration`, `drep-update`, and `drep-retirement`.
`persistent` defaults to true. Persistence requires a durable destination
queue and suitable broker configuration; a persistent message by itself is
not a durability guarantee.

The publisher enables [publisher confirms](https://www.rabbitmq.com/docs/confirms)
and mandatory routing. An unroutable message is reported as a failure even if
the broker also acknowledges the publish. A broker confirmation means the
broker accepted the message, not that a consumer processed it. A negative
confirmation, closed connection, or ten-second timeout is reported on the
plugin error channel. The next event reconnects. Ambiguous failures are not
automatically retried because republishing can duplicate delivery. Adder has
no persistent output queue, so pending events may be lost on restart. See the
[RabbitMQ publisher guide](https://www.rabbitmq.com/docs/publishers).

For a live test, declare a durable queue, configure the default exchange and
its name as `routing-key`, publish a selected event, and consume its JSON.
Then use an unbound routing key and verify the mandatory return is reported.
