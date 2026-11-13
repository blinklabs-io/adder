# Restart-safe webhook delivery

Set `plugins.output.webhook.state-path` to a persistent SQLite file to keep
webhook deliveries that have entered the output's queue across restarts.

```yaml
output: webhook
plugins:
  output:
    webhook:
      url: "https://example.org/adder-hook"
      format: "adder"
      state-path: "/var/lib/adder/webhook.sqlite"
      max-pending: 1024
```

With `state-path`, Adder formats each event once, appends the request body to
SQLite, and then sends it. The append is a [SQLite transaction](https://www.sqlite.org/lang_transaction.html).
After an HTTP 2xx response, the output explicitly removes that entry. An
HTTP 2xx response means the receiver accepted the request according to
[HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110.html); it does not
prove that the receiver finished downstream work. On restart, `Start` replays
pending entries in their original order before it returns. If replay cannot
complete, startup fails and the pending entries remain in SQLite.
Durable mode rejects redirects that change POST to a bodyless GET; 307 and
308 redirects can preserve the request body.

The existing retry policy applies to each delivery run: one initial attempt
and up to three retries for transient failures. If a terminal response or
exhausted retry limit prevents delivery, the output reports a terminal
failure, stops the pipeline, and leaves the entry in SQLite. Restarting with
the same URL and format retries it. Later events already accepted into the
output buffer are persisted during a normal stop. A process crash can still
lose events upstream of the SQLite append, including events in pipeline or
output channels; this option does not make the chainsync cursor and webhook
append one atomic transaction.

The queue has a hard `max-pending` entry limit (minimum 11, default 1024)
and a 16 MiB limit per encoded entry. `state-path` must be an on-disk SQLite
file, not an in-memory database.
On Unix, place the state file in a directory that is not group- or
world-writable. Adder restricts the database and any existing SQLite journal
sidecars to owner-only file permissions before opening the store and rejects
a symlink as the immediate parent directory. The database file itself cannot
be a symlink on any platform.
When it fills or SQLite cannot persist an event, the output fails rather than
silently discarding it. Pending entries have no automatic age expiry; they
remain until a 2xx acknowledgement or explicit operator removal. A delivered
entry is deleted immediately. Use one Adder process per `state-path`, and
preserve the database file through restarts. A URL or format change is
rejected for an existing state file to prevent old deliveries from being sent
to a different destination. Use a separate state file for a new destination.

Each queued delivery carries a stable `X-Adder-Delivery-ID` header across
retries and restarts. If a process stops after the receiver accepts a request
but before SQLite records its acknowledgement, replay can send it again.
Receivers can deduplicate by this header; the output provides at-least-once
attempts for persisted entries, not exactly-once effects. A rollback event
is queued after earlier block events and does not erase them. Consumers must
apply the rollback to their own state.

To verify recovery, configure a test receiver to return 500, send an event,
and confirm the output fails while the entry remains in the SQLite file.
Restart with the receiver returning 200, then check it receives the same
`X-Adder-Delivery-ID`. Query the SQLite file with
`SELECT COUNT(*) FROM queue WHERE name = 'webhook.pending';` and confirm it
returns zero. The package tests cover this sequence, graceful persistence of
buffered events, destination identity, and ordered SQLite replay without an
external service.
