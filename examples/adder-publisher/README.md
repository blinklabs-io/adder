# Adder Publisher Example

This example demonstrates a basic Adder pipeline that connects to a Cardano node using ChainSync and publishes all events.

## Description

This is the simplest example of using Adder as a library. It:
- Connects to a Cardano node via Node-to-Client ChainSync protocol
- Receives and logs all blockchain events
- Auto-reconnects on connection loss
- Starts syncing from the current chain tip

## Configuration

The example uses environment variables for configuration:

- `CARDANO_NODE_SOCKET_PATH`: Path to the Cardano node socket (default: `/ipc/node.socket`)
- `CARDANO_NODE_MAGIC`: Network magic number (default: `764824073` for mainnet)

### Network Magic Values

- Mainnet: `764824073`
- Preview: `2`
- Preprod: `1`

Go snippets above are option fragments; edit [main.go](main.go) to use them.
Placeholder identifiers such as `pool1...` must be replaced.

## Running

### Using a Local Node

If you have a synced Cardano node running locally:

```bash
export CARDANO_NODE_SOCKET_PATH=/path/to/node.socket
export CARDANO_NODE_MAGIC=764824073
go run main.go
```

### Using a Remote Node

Alternatively, you can uncomment the `WithAddress` line in `main.go` to connect to a remote node:

```go
// Uncomment this line:
input_chainsync.WithAddress("52.15.49.197:3001"),
// And comment out:
// input_chainsync.WithSocketPath(cfg.SocketPath),
```

Then run:

```bash
go run main.go
```

## Output and lifecycle

The callback logs `received event` with a `type` attribute; the status callback logs `chainsync status update`. All events emitted by this chainsync instance reach the callback while it is running.

The example handles SIGINT/SIGTERM, observes `Failed()` separately from
diagnostic `ErrorChan()` messages, and calls `Stop()` before returning.

## Code Structure

- `Config`: Holds configuration parsed from environment variables
- `main()`: Sets up the pipeline with input and output components
- `handleEvent()`: Processes each event received from the blockchain
- `updateStatus()`: Logs ChainSync status updates

## Next Steps

Check out the other examples to learn how to:
- Filter events by pool ID ([poolid-filter](../poolid-filter/))
- Filter by addresses and assets ([event-address-filter](../event-address-filter/))
