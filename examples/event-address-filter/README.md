# Event and Address Filter Example

This example demonstrates how to use multiple filters in an Adder pipeline to monitor specific addresses and asset transactions.

## Description

This example shows advanced filtering capabilities by combining:
- Event type filtering (only transactions)
- Address filtering (specific Cardano addresses)
- Asset fingerprint filtering (specific native tokens)

It's useful for building targeted indexers that only process relevant transactions.

## Configuration

The example uses environment variables for configuration:

- `CARDANO_NODE_SOCKET_PATH`: Path to the Cardano node socket (default: `/ipc/node.socket`)
- `CARDANO_NODE_MAGIC`: Network magic number (default: `764824073` for preview network)

By default, this example connects to a remote IOG Cardano node. To use a local socket instead, uncomment the `WithSocketPath` line and comment out the `WithAddress` line in `main.go`.

## Filters

### Event Type Filter

Filters events to only include transactions:
```go
filter_event.WithTypes([]string{"chainsync.transaction"})
```

### Address Filter

Monitors a specific Cardano address:
```go
filter_chainsync.WithAddresses(
    []string{
        "addr1q93l79hdpvaeqnnmdkshmr4mpjvxnacqxs967keht465tt2dn0z9uhgereqgjsw33ka6c8tu5um7hqsnf5fd50fge9gq4lu2ql",
    },
)
```

### Asset Fingerprint Filter

Additionally filters for transactions involving specific native assets. The example monitors DJED stablecoin:
```go
filter_chainsync.WithAssetFingerprints(
    []string{"asset15f3ymkjafxxeunv5gtdl54g5qs8ty9k84tq94x"}, // DJED
)
```

View the DJED asset on [cexplorer](https://cexplorer.io/asset/asset15f3ymkjafxxeunv5gtdl54g5qs8ty9k84tq94x).

## Customization

### Monitor Your Own Address

Replace the address in the `WithAddresses` call:
```go
filter_chainsync.WithAddresses(
    []string{
        "addr1...", // Your address here
    },
)
```

### Monitor Different Assets

Update the `WithAssetFingerprints` parameter:
```go
filter_chainsync.WithAssetFingerprints(
    []string{
        "asset1...", // Your asset fingerprint
        "asset2...", // Another asset
    },
)
```

To disable asset filtering, comment out or remove the `WithAssetFingerprints` line.

### Filter Other Event Types

Available event types include:
- `chainsync.block`
- `chainsync.transaction`
- `chainsync.rollback`

## Running

```bash
go run main.go
```

Or with custom environment variables:

```bash
export CARDANO_NODE_SOCKET_PATH=/path/to/node.socket
export CARDANO_NODE_MAGIC=764824073
go run main.go
```

## Expected Output

The program will only output transaction events involving the specified address and/or assets:
```
ChainSync status update: {Status: syncing, Tip: 12345678}
Received event: chainsync.transaction (involving filtered address/asset)
...
```

## Use Cases

This pattern is useful for:
- Wallet transaction monitors
- Payment notification systems
- Token-specific analytics
- DApp activity tracking
- NFT marketplace indexers
- Stablecoin transaction monitoring

## Code Structure

- `Config`: Holds configuration parsed from environment variables
- `main()`: Sets up the pipeline with input, multiple filters, and output
- `filterEvent`: Event type filter (transactions only)
- `filterChainsync`: ChainSync filter for addresses and assets
- `handleEvent()`: Processes filtered events
- `updateStatus()`: Logs ChainSync status updates

## Filter Order

Note that filters are applied in the order they're added to the pipeline:
1. Event type filter (transactions only)
2. ChainSync filter (address and asset)

This means the address/asset filter only sees transaction events.

## Next Steps

- Add database storage to build a transaction history
- Combine with pool ID filtering for more complex use cases
- Implement custom event handlers for specific transaction types
- Add webhook notifications for matched transactions
