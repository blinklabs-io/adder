# Managed customer routing

Managed mode gives one ChainSync input independent customer filter lanes and named output destinations. It is opt-in: ordinary Adder CLI and embedded pipelines retain their existing filters, output backpressure, configuration precedence and `/events` API.

Start with the [five-customer live smoke script](managed-routing-smoke.md). Managed mode is selected with `--managed-config-dir`. Its other operator flags are `--managed-credentials-dir`, `--managed-log-dir`, `--managed-token-file`, `--managed-address` (default `127.0.0.1:8081`), `--managed-allow-hosts` and `--managed-allow-local-targets`. The last flag is an explicit exception for loopback test receivers. Network targets otherwise require HTTPS, an allowed hostname and public destination addresses. The configuration API binds only literal loopback addresses. Place any remote operator access behind a separately administered secure tunnel.

The normal HTTP API and `/events` observer are not started in managed mode. Global filters are rejected. Use ChainSync with `intersect-tip=true`, `auto-reconnect=true`, no `state-path` and no explicit intersect point. Dingo and a public Cardano peer use the same input and route configuration. On process restart Adder starts at the then-current tip; events between runs can be lost. In-process reconnect can replay recent events or emit rollback. Neither the router nor its enabled plugins persist a delivery queue.

## Files and publication

The operator is the sole writer. Adder never writes configuration or credential files. Publish complete files by atomic replacement, write credentials first, customer files second, and `manifest.json` last. Increment its generation and each changed customer's revision. File references are basenames confined to their respective directories. A valid initial bundle with at least one customer is required before the source starts.

```json
{"generation":1,"customers":[{"id":"u1","revision":1,"file":"u1.json"}]}
```

A customer file repeats its identity and revision. Every rule names one canonical input event type and chooses `all` or one destination ID in that customer file.

```json
{
  "id":"u1","revision":1,
  "destinations":[{"id":"local","plugin":"log","options":{"path":"u1.jsonl","format":"json"}}],
  "filters":[
    {"id":"blocks","event_type":"input.block","destination":"all"},
    {"id":"transactions","event_type":"input.transaction","destination":"local"},
    {"id":"rollbacks","event_type":"input.rollback","destination":"all"}
  ]
}
```

Optional rule `criteria` contains arrays named `addresses`, `policies`, `asset_fingerprints`, `pool_ids`, and `drep_ids`. Criteria retain Adder's existing semantics: address, policy and asset criteria combine with AND; pool and DRep identities combine with OR when both are present. Unsupported event/criterion pairs are rejected. Block rules accept pool criteria; transaction rules accept all criteria; governance rules accept address, pool and DRep criteria; DRep certificate rules accept DRep criteria; rollback has no Cardano criteria. All seven canonical input event types are supported by the matcher.

A webhook destination uses versioned credential references for its URL and any basic authentication values:

```json
{
  "id":"alerts","plugin":"webhook","options":{"format":"adder"},
  "credentials":{"url":{"file":"u1-alert-url-v1.json","version":1}},
  "limit_group":"alerts.example.com","interval_ms":1000
}
```

The referenced restricted file contains `{"customer_id":"u1","version":1,"value":"https://alerts.example.com/incoming/SECRET"}`. Its mode must be 0600 or stricter. Use private operator-owned directories. Credential contents cannot change under an existing applied reference version, even when the customer revision advances. Publish a new version to rotate a credential. References cannot be shared across customers. Keep a protected known-good bundle outside the live publication directory; after a rejected update, restore it or publish a corrected revision above the applied revision. Last-good configuration exists only in memory: invalid published files prevent a fresh process from starting.

## Configuration API

All routes, including status, health and metrics, require `Authorization: Bearer <operator token>`. The token file must be restricted to its owner and contain at least 32 characters. Never put the token on a URL. API requests read files; they do not accept replacement configuration bodies.

| Request | Body | Meaning |
| --- | --- | --- |
| `POST /v1/config/reload` | `{"generation":2}` | Validate and prepare the entire manifest, then switch it together. Required for customer additions/removals. |
| `POST /v1/config/customers/u1/reload` | `{"revision":2}` | Reload an existing customer with an unchanged manifest roster. Other customers keep their applied revisions. |
| `GET /v1/config/status` | none | Observed manifest generation, last fully applied reload-all generation, per-customer revisions/cutovers, validation error, destination state, counters and capabilities. |
| `GET /healthcheck` | none | Router readiness; source connectivity and destination degradation are separate status signals. |
| `GET /metrics` | none | Aggregate managed counters with no customer or credential labels. |

Reloads are serialized and have a preparation deadline. A failure leaves the existing configuration active and cleans up staged plugins. Failed preparation cancels and joins staged instances, allowing up to five seconds for cleanup; any unfinished cleanup stays charged to the work budget. Repeating identical content at an applied revision is idempotent; changed content at that revision and stale revisions are errors. Status may deliberately show mixed customer revisions after targeted reloads. The latest applied customer and credential-binding revisions are retained after removal for the lifetime of the run. The customer and destination identity limits therefore also bound distinct identities used during that run; re-adding an identity cannot roll its revision back. Restart clears this in-memory history.

A successful response includes `cutover_occurrence_id`, the last source occurrence assigned before the switch. It is returned even if no event arrives afterward. Repeating the same applied revision or reload-all generation returns its original cutover. It is not a drain acknowledgment, an assertion that the revision handled an event, or evidence of external delivery. Events already admitted to a customer retain their configuration revision. Removing a customer/destination or rotating credentials revokes the old destination generation even for old-revision events waiting in a customer queue. Already submitted work can have an uncertain outcome.

## Concurrency, limits and delivery evidence

The dispatcher assigns monotonically increasing run-local occurrence IDs, including for rollback and repeated identical events. It performs bounded admission accounting, then offers independently to each customer queue. It never runs a customer's matcher or sends to an output plugin. Each customer has one filter worker, preserving its input order. Overlapping rules coalesce into one submission per occurrence and named destination, with sorted matching rule IDs. Destination workers are independent. A match is offered immediately; there is no periodic flush timer.

Defaults allow 32 customers, 32 rules per customer, eight destinations per customer and 512 active/staged/retiring plugin instances, subject to the total work budget. Input and destination queues each hold at most 256 items. An event's JSON representation is capped at 1 MiB. Each queue has up to 16 MiB of accounted retained data, with a conservative charge of eight times encoded event size plus fixed overhead. Queue capacity is reserved before a configuration switch; one slow lane cannot consume another's allowance. The process-wide managed reservation limit is 512 MiB. Configuration objects and plugin channel slots also consume this budget, and retired configurations remain charged while queued events reference them. These are routing-work bounds, not a promise about total process RSS, Go runtime overhead, input-plugin buffers or transient encoder allocations. Operators embedding the router can set explicit limits through `managed.Options`.

A full queue rejects its new item and increments that lane's drop counters. Reloads that cannot reserve resources are rejected. HTTP attempts have a five-second deadline; managed destination processing has a fifteen-second budget and the existing webhook retry count remains bounded. `429` and `Retry-After` delay every destination in that canonical target authority's shared limit group; another origin progresses independently. The configured interval is a minimum spacing between attempts, including retries. Choose it to meet the provider's documented quota. `format=discord` requires at least two seconds, and server-provided waits remain authoritative. Authority names are lowercase and omit the scheme's default port, so case or an explicit `:443` cannot split a shared quota. All destinations for one authority must use the same interval. The pilot supports authority-scoped quotas; outputs requiring quotas shared across distinct authorities need an adapter before enablement.

Counters separate `input_dropped`, `matched` (per rule), `destination_dropped`, `submitted_to_plugin`, `plugin_errors`, `unknown_outcomes` and `canceled`. Customer counters cover admission and matching; destination counters cover submissions and outcomes. Process totals also retain counts from removed destinations. Plugin-channel acceptance is never called external delivery. The completion observer sequences one outstanding plugin event at a time; it only observes processing completion. External outcomes remain unknown, including accept-then-disconnect failures that may cause duplicate sends during retry. HTTP or log write errors remain distinct diagnostics. There is no durable deduplication or exactly-once delivery.

Metadata diagnostics use `component=managed.router`: `filter_configured` once per applied rule, debug-level `filter_matched`, submission/drop/cancellation actions, and shutdown totals. They contain opaque customer/rule IDs, revision and occurrence ID, never credential values or event payloads. The `log` output is a deliberate payload destination for trusted local operators. All routing diagnostics share an asynchronous metadata queue of at most 1,024 entries, reduced for smaller process budgets. Its reserved storage remains charged to the same process budget until cleanup completes. A slow sink does not hold the dispatch lock or customer workers; `diagnostic_dropped` counts diagnostic queue overflow separately from event loss. If a sink remains blocked at shutdown, its one worker may finish the finite queue later; the same router cannot restart until that cleanup finishes.

Shutdown first stops source admission, drains admitted routing work for up to twenty seconds, then cancels remaining work. Plugin senders are joined before plugins stop. Submitted operations may remain uncertain even when plugin processing finishes. Readiness is false during startup/shutdown; one degraded destination does not terminate another customer's route or reconnect the input.

## Output capability inventory

The registry currently contains 22 output names: the `aws` package registers both `sns` and `sqs`. The status API returns a disposition for every registered output.

| Output | Managed disposition |
| --- | --- |
| `log` | Enabled for trusted local JSON append files whose basename starts with the owning customer ID followed by a dot (for example, `u1.events.jsonl`). The actual file open is confined and verifies regular-file identity. No arbitrary output paths or stdout payload destinations. |
| `webhook` | Enabled for one target, `adder` or `discord` format, instance-owned credentials, confined transport, authority quota and bounded retries. Redirects, environment proxies, private/reserved destinations and DNS changes into denied addresses are rejected. `state-path`, TLS verification bypass and undeclared options are rejected. |
| `slack`, `telegram`, `discord`, `matrix`, `signal`, `push` | Rejected: bot/server lifecycle, multi-target sending or provider-account quota needs a dedicated adapter. |
| `sns`, `sqs`, `pubsub`, `s3`, `kafka`, `nats`, `mqtt`, `rabbitmq`, `redis`, `postgres`, `clickhouse`, `elasticsearch` | Rejected: SDK/broker buffers, retries, endpoint discovery, credentials and quota boundaries need a dedicated adapter. |
| `notify`, `notify-json` | Rejected: desktop and file side effects are not customer-isolated. |

Future output adapters must establish lifecycle, allowed events, fanout, retained buffers, rate limits, credentials, target restrictions and observable outcomes. The registry alone is not a hot-reload capability claim.
