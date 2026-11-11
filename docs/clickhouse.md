# ClickHouse output

The `clickhouse` output creates `adder_events_v1` in the selected ClickHouse
database and writes selected events as JSONEachRow batches over HTTP. The
table has `schema_version UInt8`, `event_hash String`, `event_type
LowCardinality(String)`, `event_time DateTime64(9, 'UTC')`, and `event_json
String`, partitioned by event month in a MergeTree. The versioned table name
reserves schema changes for a new table and explicit migration.

```yaml
output: clickhouse
plugins:
  output:
    clickhouse:
      url: https://clickhouse.example.org:8443
      username: adder
      password: YOUR_PASSWORD
      event-types: blocks,transactions,rollbacks,governance
```

The output flushes at 100 events or one second, whichever comes first. Each
flush is one `INSERT` statement; successful HTTP completion acknowledges that
batch, not individual downstream queries. Stop drains the plugin's accepted
input buffer and attempts one final flush with a ten-second deadline. A failed
batch is logged and sent to the error channel when space is available; it is
discarded locally. There is no durable queue. Ambiguous failures are not
automatically retried because repeating a batch can add duplicate rows.

Every received row is retained, including identical duplicates. The
`event_hash` is SHA-256 of the full JSON for comparison, not a unique key.
ClickHouse insert deduplication is disabled for this output, using the current
`deduplicate_insert=disable` setting and the older `insert_deduplicate=0`
setting, so an identical
block reapplied after rollback remains a distinct occurrence. Rollbacks are
ordinary `input.rollback` rows; older rows are not deleted. ClickHouse sort
order is analytical and does not establish a global chain replay order.

The output uses [ClickHouse HTTP POST queries](https://clickhouse.com/docs/concepts/features/interfaces/http),
[`JSONEachRow`](https://clickhouse.com/docs/reference/formats/JSON/JSONEachRow),
and [MergeTree](https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/mergetree).
It forces `async_insert=0` so HTTP success reflects a completed insert even
when the ClickHouse user profile enables asynchronous inserts. See the current
[deduplication setting](https://clickhouse.com/docs/reference/settings/session-settings/deduplicate-insert)
and [asynchronous insert behavior](https://clickhouse.com/docs/concepts/features/operations/insert/asyncinserts).
The HTTP `wait_end_of_query=1` setting and response-body inspection detect
late exceptions described in the HTTP API documentation. For a live check,
start ClickHouse, select this output, publish a block and rollback, and query
`SELECT event_type, event_json FROM adder_events_v1 ORDER BY event_time`.
