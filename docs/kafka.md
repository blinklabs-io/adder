# Kafka output

The `kafka` output publishes selected Adder events as JSON to one Kafka topic
using [franz-go v1.22.3](https://github.com/twmb/franz-go/releases/tag/v1.22.3).
Create the topic before starting Adder.

```yaml
output: kafka
plugins:
  output:
    kafka:
      brokers: "127.0.0.1:9092"
      topic: "adder.events"
      event-types: "blocks,rollbacks,governance"
```

`brokers` is a comma-separated list of bootstrap `host:port` addresses.
Set `tls: true` for TLS. For SASL PLAIN, set `username` and `password` and
enable TLS; supply secrets through secret-managed configuration. The default
event types are `blocks,rollbacks,governance`; optional types are
`transactions`, `drep-registration`, `drep-update`, and `drep-retirement`.

Each record value is the complete Adder event JSON. The key is always
`adder.events`. With an unchanged topic partition count, records from one
Adder producer go to one partition and retain their sequence across event
types. Do not expand the topic's partitions while relying on that ordering.
Multiple Adder processes do not have a shared total order. The producer
requests acknowledgement from all in-sync replicas (`acks=all`) and uses
franz-go's idempotent producer for
its internal retries. See the current [Apache Kafka producer configuration](https://kafka.apache.org/44/configuration/producer-configs/)
for the acknowledgement and idempotence semantics.

Adder waits for each record's result before reading the next selected event.
A send has a ten-second deadline. Rejected or uncertain delivery is reported
on the plugin error channel. Adder does not resend a failed record at the
application level: broker receipt may have succeeded before a reply was lost,
and a new send would not share the original producer sequence number. The
client may reconnect for later events. Buffered events are not recovered
after shutdown or restart. A successful acknowledgement confirms the broker's
configured replication policy, not consumer processing.

For a live check, create a topic with at least two partitions, publish block
and rollback events, and consume from the beginning to inspect record keys,
values, and partition ordering. Then stop the broker and confirm Adder reports
an output error. The package test uses franz-go's [in-process Kafka protocol broker](https://github.com/twmb/franz-go/blob/master/docs/testing.md)
to inspect the produce request, topic ID, key, JSON value, and `acks=-1`.
