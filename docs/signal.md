# Signal output

The `signal` output sends selected Adder events through a separately managed
[`signal-cli` HTTP JSON-RPC daemon](https://github.com/AsamK/signal-cli/blob/master/man/signal-cli-jsonrpc.5.adoc).
Signal does not provide an official bot API. Run a maintained signal-cli daemon
with a linked or registered account, for example `signal-cli -a ACCOUNT daemon --http=127.0.0.1:8081`.

Configure `plugins.output.signal.endpoint` as the daemon's `/api/v1/rpc` URL,
`recipients` (required, with no default) as comma-separated recipient numbers or service IDs, and `account`
when using a multi-account daemon. Plain HTTP is accepted only on loopback;
use HTTPS for a remote daemon. The default endpoint is
`http://127.0.0.1:8081/api/v1/rpc`. The default event selection is
`blocks,rollbacks,governance`; `transactions`, `drep-registration`,
`drep-update`, and `drep-retirement` are also available.

Each message contains event JSON. An HTTP 200 response is accepted only when
the JSON-RPC `send` result contains a timestamp and the response ID matches the
request. Errors are reported through the output error channel. Requests are
not automatically retried because a timeout can occur after Signal sends the
message; retrying could duplicate it. Pending events are not retained across
Adder restarts. A successful RPC result means signal-cli accepted the send,
not that the recipient read the message.

For a live check, use a separate Signal account and recipient, send a selected
event, and confirm the message arrives. The local tests use an HTTP test daemon
to verify the documented JSON-RPC request and response contract.
