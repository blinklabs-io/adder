# PostgreSQL output

The `postgres` output appends selected Adder events to `adder_events` in an
existing PostgreSQL database. It creates the table and time index at startup.

```yaml
plugins:
  output:
    postgres:
      database-url: postgres://adder:secret@localhost:5432/adder
      event-types: blocks,transactions,rollbacks,governance
```

The schema has `arrival_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY`,
`event_hash text`, `schema_version smallint` (currently 1),
`event_type text`, `event_time timestamptz`, and `event_json jsonb`. All
selected events, including `input.rollback`, are append-only records. A
rollback does not erase historical rows; consumers apply rollback records to
derive active chain state by replaying rows in `arrival_id` order for a single
output writer, including when event timestamps repeat or go backward. Multiple
concurrent writers do not provide a global chain order. Future incompatible storage shapes require a new
schema version and an explicit migration.

Each event is one `INSERT` statement and thus one PostgreSQL transaction. There
is no plugin batch or flush timer. The shared input channel buffers at most ten
events; upstream sends then wait for space. The SHA-256 of the complete JSON
is stored in `event_hash` for comparison. Every received event is retained,
including identical duplicates. This preserves a block that is reapplied after
a rollback even if its JSON is identical to the earlier block. Adder receives
no upstream occurrence ID that can distinguish retransmission from reapplication.

A statement has a ten-second deadline. Failed inserts are reported through the
plugin error channel and are not retained locally for retry. On stop, the
in-flight statement is canceled and queued events are discarded. The database
account needs ownership or suitable DDL privileges for startup schema setup,
`INSERT` on the table, and use of the identity sequence.
See the current [PostgreSQL `INSERT` and `ON CONFLICT` documentation](https://www.postgresql.org/docs/current/sql-insert.html),
[`jsonb` documentation](https://www.postgresql.org/docs/current/datatype-json.html),
and [pgx v5 driver](https://github.com/jackc/pgx/tree/master/stdlib).

To run the optional live integration test, point `ADDER_TEST_POSTGRES_URL` at
a dedicated test database and run `go test ./output/postgres`. The test rolls
back its schema and event inserts at the end.
