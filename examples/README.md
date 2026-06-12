# Adder Library Examples

This directory contains example applications demonstrating how to use [Adder](https://github.com/blinklabs-io/adder) as a library to build custom Cardano blockchain indexers.

Adder communicates with a Cardano Node using the ChainSync protocol to observe and process blockchain events in real-time.

## Prerequisites

### Cardano Node Access

To run these examples, you'll need access to a fully synced Cardano node. You have several options:

1. **Local Node**: Run your own Cardano node and connect via Unix socket
2. **Remote Node**: Connect to a remote node via TCP/IP
3. **Public Infrastructure**: Use publicly available Cardano nodes (some examples are pre-configured with IOG nodes)

### Environment Variables

The examples use the following environment variables for configuration:

- `CARDANO_NODE_SOCKET_PATH`: Path to the Cardano node Unix socket (e.g., `/path/to/node.socket`)
- `CARDANO_NODE_MAGIC`: Network magic number that identifies the Cardano network

#### Network Magic Values

| Network | Magic Number |
|---------|--------------|
| Mainnet | 764824073 |
| Preview | 2 |
| Preprod | 1 |

## Examples

### 1. [adder-publisher](./adder-publisher/)

**Difficulty: Beginner**

The simplest Adder example. Connects to a Cardano node and logs all blockchain events.

**What you'll learn:**
- Basic Adder pipeline setup
- Connecting to a Cardano node
- Handling blockchain events
- ChainSync status monitoring

**Use case:** Understanding the Adder event model and testing your node connection.

```bash
cd adder-publisher
go run main.go
```

### 2. [poolid-filter](./poolid-filter/)

**Difficulty: Beginner**

Demonstrates filtering events by stake pool ID to monitor specific pools.

**What you'll learn:**
- Adding filters to the pipeline
- Pool-specific event filtering
- Monitoring delegations and block production

**Use case:** Building pool monitoring tools, delegation trackers, or pool performance dashboards.

```bash
cd poolid-filter
go run main.go
```

### 3. [event-address-filter](./event-address-filter/)

**Difficulty: Intermediate**

Shows how to combine multiple filters to monitor specific addresses and native assets.

**What you'll learn:**
- Using multiple filters in a pipeline
- Event type filtering
- Address-based filtering
- Asset fingerprint filtering

**Use case:** Wallet monitors, payment notifications, token analytics, NFT tracking.

```bash
cd event-address-filter
go run main.go
```

## Running the Examples

Each example can be run independently. Navigate to the example directory and use Go to run it:

```bash
cd <example-directory>
go run main.go
```

### Customizing Network Configuration

To use a different network or node:

```bash
export CARDANO_NODE_SOCKET_PATH=/path/to/your/node.socket
export CARDANO_NODE_MAGIC=764824073
cd adder-publisher
go run main.go
```

### Building Binaries

To build an executable binary:

```bash
cd <example-directory>
go build -o my-indexer main.go
./my-indexer
```

## Common Patterns

### Pipeline Structure

All examples follow a similar pattern:

1. **Configuration**: Parse environment variables
2. **Create Pipeline**: Initialize a new Adder pipeline
3. **Configure Input**: Set up ChainSync connection
4. **Add Filters** (optional): Filter events by type, address, pool, etc.
5. **Configure Output**: Define how to handle events
6. **Start Pipeline**: Begin processing events
7. **Error Handling**: Monitor for pipeline errors

### Event Handlers

Each example includes a `handleEvent` function where you can implement custom logic:

```go
func handleEvent(evt event.Event) error {
    // Your custom logic here
    // e.g., save to database, send notifications, etc.
    return nil
}
```

### Status Updates

Monitor sync progress with `updateStatus`:

```go
func updateStatus(status input_chainsync.ChainSyncStatus) {
    // Log or display sync status
}
```

## Building Your Own Indexer

Start with the `adder-publisher` example and gradually add features:

1. **Start Simple**: Get events flowing with the basic publisher
2. **Add Filters**: Use filters to focus on relevant events
3. **Implement Logic**: Add your custom event handling
4. **Add Storage**: Integrate a database for persistence
5. **Add Outputs**: Use multiple outputs (logging, webhooks, etc.)

## Troubleshooting

### Connection Issues

- Verify `CARDANO_NODE_SOCKET_PATH` points to the correct socket
- Ensure the Cardano node is running and fully synced
- Check that `CARDANO_NODE_MAGIC` matches your network

### Sync Performance

- Start with `WithIntersectTip(true)` to sync from the current tip
- For historical data, use `WithBulkMode(true)` for faster processing
- Consider using filters early to reduce event volume

### Memory Usage

- Use specific filters to reduce event volume
- Implement batching in your event handler
- Consider using bulk mode for historical syncing

## Additional Resources

- [Adder Documentation](https://github.com/blinklabs-io/adder)
- [Cardano Developer Portal](https://developers.cardano.org/)
- [Ouroboros Network Specification](https://ouroboros-network.cardano.intersectmbo.org/)

## Contributing

Found an issue or want to add a new example? Contributions are welcome!

1. Fork the repository
2. Create your feature branch
3. Add or modify examples
4. Update documentation
5. Submit a pull request

## License

These examples are licensed under the Apache License 2.0. See the [LICENSE](../LICENSE) file for details.
