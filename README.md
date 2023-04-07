# Snek

Snek is a tool for tailing the Cardano blockchain and emitting events for each
block and transaction that it sees.

## How it works

Input can be a local or remote Cardano full node, using either NtC (local UNIX
socket, TCP over socat) or NtN to remote nodes.

Events are created with a simple schema.

```json
{
    "type": "event type",
    "timestamp": "wall clock timestamp of event",
    "payload": "the full event specific payload"
}
```

The chainsync input produces three event types: `block`, `rollback`, and
`transaction`. Each type has a unique payload.

block:
```json
{
    "payload": {
        "blockNumber": 123,
        "blockHash": "abcd123...",
        "slotNumber": 1234567,
        "blockCbor": "85828a1a000995c21..."
    }
}
```

rollback:
```json
{
    "payload": {
        "blockHash": "abcd123...",
        "slotNumber": 1234567
    }
}
```

transaction:
```json
{
    "payload": {
        "blockNumber": 123,
        "blockHash": "abcd123...",
        "slotNumber": 1234567,
        "transactionHash": "0deadbeef123...",
        "transactionCbor": "a500828258200a1ad..."
    }
}
```

Each event is output individually. The log output prints each event to stdout
using Uber's `Zap` logging library.

## Example usage

### Native using remote node

```bash
export CARDANO_NETWORK=preview \
  CARDANO_NODE_SOCKET_TCP_HOST=preview-node.world.dev.cardano.org \
  CARDANO_NODE_SOCKET_TCP_PORT=30002 \
  CARDANO_NODE_USE_NTN=true
./snek 
```

### In Docker using local node

First, follow the instructions for
[Running a Cardano Node](https://github.com/blinklabs-io/docker-cardano-node#running-a-cardano-node)
in Docker.

```bash
docker run --rm -ti \
  -v node-ipc:/node-ipc \
  ghcr.io/blinklabs-io/snek:main
```
