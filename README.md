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

## Configuration

Snek supports multiple configuration methods for versatility: commandline arguments, YAML config file,
and environment variables (in that order).

You can get a list of all available commandline arguments by using the `-h`/`-help` flag.

```bash
$ ./snek -h
Usage of snek:
  -config string
        path to config file to load
  -input string
        input plugin to use, 'list' to show available (default "chainsync")
  -input-chainsync-address string
        specifies the TCP address of the node to connect to
...
  -output string
        output plugin to use, 'list' to show available (default "log")
  -output-log-level string
        specifies the log level to use (default "info")
```

Each commandline argument (other than `-config`) has a corresponding environment variable. For example,
the `-input` option has the `INPUT` environment variable, the `-input-chainsync-address` option has the
`INPUT_CHAINSYNC_ADDRESS` environment variable, and `-output` has `OUTPUT`.

You can also specify each option in the config file.

```yaml
input: chainsync

output: log
```

Plugin arguments can be specified under a special top-level key in the config file.

```yaml
plugins:
  input:
    chainsync:
      network: preview
      address: preview-node.world.dev.cardano.org
      port: 30002
      use-ntn: true

  output:
    log:
      level: info
```

## Example usage

### Native using remote node

```bash
export INPUT_CHAINSYNC_NETWORK=preview \
  INPUT_CHAINSYNC_ADDRESS=preview-node.world.dev.cardano.org \
  INPUT_CHAINSYNC_PORT=30002 \
  INPUT_CHAINSYNC_USE_NTN=true \
  INPUT_CHAINSYNC_INTERSECT_TIP=true
./snek 
```

Alternatively using equivalent commandline options:

```bash
./snek \
  -input-chainsync-network preview \
  -input-chainsync-address preview-node.world.dev.cardano.org \
  -input-chainsync-port 30002 \
  -input-chainsync-use-ntn \
  -input-chainsync-intersect-tip
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
