# Pool ID Filter Example

This example demonstrates how to filter blockchain events by stake pool IDs using Adder.

## Description

This example adds the Cardano filter. It matches block issuers and supported transaction/governance pool references. Other payload types, such as rollbacks, pass through when no applicable restriction is configured. It shows how to:
- Filter events by stake pool ID
- Match supported pool certificates and block production; no reward event is emitted
- Use ChainSync filters in the pipeline

## Configuration

The example uses environment variables for configuration:

- `CARDANO_NODE_SOCKET_PATH`: Parsed but unused until the code is switched to `WithSocketPath` (default: `/ipc/node.socket`).
- `CARDANO_NODE_MAGIC`: Network magic number (default: `764824073` for mainnet)

By default, this example connects to a remote IOG Cardano node. To use a local socket instead, uncomment the `WithSocketPath` line and comment out the `WithAddress` line in `main.go`.

## Pool IDs

The example is pre-configured to monitor two stake pools:
- `pool16agnvfan65ypnswgg6rml52lqtcqe5guxltexkn82sqgj2crqtx` ([View on cexplorer](https://cexplorer.io/pool/pool16agnvfan65ypnswgg6rml52lqtcqe5guxltexkn82sqgj2crqtx))
- `pool12t3zmafwjqms7cuun86uwc8se4na07r3e5xswe86u37djr5f0lx` ([View on cexplorer](https://cexplorer.io/pool/pool12t3zmafwjqms7cuun86uwc8se4na07r3e5xswe86u37djr5f0lx))

To monitor different pools, modify the `WithPoolIds` parameter in `main.go`:

```go
filter_chainsync.WithPoolIds(
    []string{
        "pool1...", // Your pool ID here
        "pool2...", // Another pool ID
    },
),
```

Go snippets above are option fragments; edit [main.go](main.go) to use them.
Placeholder identifiers such as `pool1...` must be replaced.

## Running

```bash
go run main.go
```

Or with custom environment variables:

```bash
export CARDANO_NODE_MAGIC=764824073
go run main.go
```

## Output and lifecycle

The callback logs `received event` with a `type` attribute; the status callback logs `chainsync status update`. The pool filter applies only to supported payload types; use an event-type filter if rollbacks and other pass-through events are unwanted.

The example handles SIGINT/SIGTERM, observes `Failed()` separately from
diagnostic `ErrorChan()` messages, and calls `Stop()` before returning.

## Use Cases

This filter is useful for:
- Monitoring your own stake pools
- Building pool performance dashboards
- Tracking delegations to specific pools
- Analyzing block production for pools of interest
- Creating pool-specific notifications

## Code Structure

- `Config`: Holds configuration parsed from environment variables
- `main()`: Sets up the pipeline with input, filter, and output components
- `filterChainsync`: ChainSync filter configured with specific pool IDs
- `handleEvent()`: Processes filtered events
- `updateStatus()`: Logs ChainSync status updates

## Next Steps

- Combine with the event filter example to filter by both pool and event type
- Add database storage to persist pool-related events
- Build a web dashboard to display pool metrics
