# Slack notification output

The `slack` output posts selected Adder events to one or more Slack incoming
webhooks. Each webhook URL is an authentication secret tied to a Slack
destination. Create one for each channel using Slack's
[incoming webhook setup](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/)
and keep the URLs private.

Slack's current public incoming webhook API is unversioned. The local tests
assert its documented JSON `text` POST, HTTP 200 `ok` success response, and
HTTP 429 `Retry-After` behavior against the
[current webhook guide](https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/)
and [rate limit reference](https://docs.slack.dev/apis/web-api/rate-limits/).

```yaml
output: slack
plugins:
  output:
    slack:
      webhook-urls: "https://hooks.slack.com/services/EXAMPLE/ONE,https://hooks.slack.com/services/EXAMPLE/TWO"
      event-types: "blocks,transactions,rollbacks,governance"
```

`webhook-urls` requires at least one HTTPS URL. `event-types` accepts one or more
of `blocks`, `transactions`, `rollbacks`, `governance`, `drep-registration`,
`drep-update`, and `drep-retirement`; the first four are enabled by default.
Adder formats each selected event with its type, slot or block
number, and relevant hash or count, then posts a JSON `text` payload to every
configured destination. Events not selected for Slack continue through the
pipeline normally.

Slack documents a successful incoming webhook response as HTTP 200 with `ok`.
Adder spaces ordinary posts to each webhook by at least one second. For HTTP
429, it reads the documented `Retry-After` seconds and retries the same message
up to three times. The cooldown remains in force for later events, including
when an event exhausts its retry budget. It also retries transient server or
transport errors with bounded backoff. Missing or invalid `Retry-After`
values fail the affected delivery and impose a one-minute destination cooldown.
Other client errors and exhausted retries appear on
the plugin error channel and in logs without exposing webhook URLs. A delay
beyond one minute is reported as a delivery failure. Later events for that
destination are skipped while more than one minute remains, then wait for the
rest of Slack's cooldown. The worker has a bounded input buffer and no durable
queue; messages still pending when the process stops are not recovered after
restart.
See Slack's [rate limit reference](https://docs.slack.dev/apis/web-api/rate-limits/).

For a live check, use a separate test webhook and send a selected chain event.
Confirm the message appears in its configured channel. The local test suite
uses a fake HTTP transport to verify the request body, response handling,
rate-limit behavior, selection, and error visibility without a Slack secret.
