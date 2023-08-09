# snek

<div align="center">
    <img src="./assets/snek-logo-with-text-horizontal.png" alt="snek Logo" width="640">
</div>

snek is a tool for tailing the Cardano blockchain and emitting events for each
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

snek supports multiple configuration methods for versatility: commandline arguments, YAML config file,
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

  output:
    log:
      level: info
```

## Filtering

snek supports filtering events before they are output using multiple criteria. An event must match all configured filters to be emitted.
Each filter supports specifying multiple possible values separated by commas. When specifying multiple values for a filter, only one of
the values specified must match an event.

You can get a list of all available filter options by using the `-h`/`-help` flag.

```bash
$ ./snek -h
Usage of snek:
...
  -filter-address string
        specifies address to filter on
  -filter-asset string
        specifies the asset fingerprint (asset1xxx) to filter on
  -filter-policy string
        specifies asset policy ID to filter on
  -filter-type string
        specifies event type to filter on
...
```

Multiple filter options can be used together, and only events matching all filters will be output.

## Example usage

### Native using remote node

```bash
export INPUT_CHAINSYNC_NETWORK=preview
./snek 
```

Alternatively using equivalent commandline options:

```bash
./snek \
  -input-chainsync-network preview
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

### Filtering

#### Filtering on event type

Only output `chainsync.transaction` event types

```bash
$ snek -filter-type chainsync.transaction
```

Only output `chainsync.rollback` and `chainsync.block` event types

```bash
$ snek -filter-type chainsync.transaction,chainsync.block
```

#### Filtering on asset policy

Only output transactions involving an asset with a particular policy ID

```bash
$ snek -filter-type chainsync.transaction -filter-policy 13aa2accf2e1561723aa26871e071fdf32c867cff7e7d50ad470d62f
```

#### Filtering on asset fingerprint

Only output transactions involving a particular asset

```bash
$ snek -filter-type chainsync.transaction -filter-asset asset108xu02ckwrfc8qs9d97mgyh4kn8gdu9w8f5sxk
```

#### Filtering on a policy ID and asset fingerprint

Only output transactions involving both a particular policy ID and a particular asset (which do not need to be related)

```bash
$ snek -filter-type chainsync.transaction -filter-asset asset108xu02ckwrfc8qs9d97mgyh4kn8gdu9w8f5sxk -filter-policy 13aa2accf2e1561723aa26871e071fdf32c867cff7e7d50ad470d62f
```

#### Filtering on an address

Only output transactions with outputs matching a particular address

```bash
$ snek -filter-type chainsync.transaction -filter-address addr1qyht4ja0zcn45qvyx477qlyp6j5ftu5ng0prt9608dxp6l2j2c79gy9l76sdg0xwhd7r0c0kna0tycz4y5s6mlenh8pq4jxtdy
```

#### Filtering on a stake address

Only output transactions with outputs matching a particular stake address

```bash
$ snek -filter-type chainsync.transaction -filter-address stake1u9f9v0z5zzlldgx58n8tklphu8mf7h4jvp2j2gddluemnssjfnkzz
```
