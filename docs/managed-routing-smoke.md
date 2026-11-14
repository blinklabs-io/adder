# Live managed-routing smoke test

Build Adder with `make build`, then run the repository-owned Python 3 script:

```sh
python3 scripts/test-managed-mainnet.py \
  --adder ./adder \
  --output-dir /tmp/adder-managed-mainnet-run
```

The output directory must be new or empty. The script creates five customer
files, each with named `blocks`, `transactions`, and `rollbacks` rules and one
local JSON `log` destination. It starts ChainSync at the public Cardano mainnet
tip with automatic reconnect enabled and no persisted cursor. The authenticated
management API uses an available loopback port and a generated restricted token
file. No customer webhook credentials are needed.

The run succeeds only after all 15 rules were configured, a single live block
occurrence matched all five customers, and the same mainnet block hash appears
at the corresponding position in every customer's event file. Receipt order,
event types, and counts are reconciled against matched occurrence IDs. Duplicate,
extra, missing, or reordered local receipts cause failure. The script sends
SIGTERM to its own Adder process, allows graceful draining, and checks the final
files. Dispatch diagnostics and exact customer/occurrence/destination/rule/revision
submissions are also reconciled. Any logged drop, plugin error, cancellation, or
uncertain submission fails this local-log run, including during shutdown. A
forced shutdown fails the run. The default observation deadline is 180
seconds; override it with `--timeout-seconds` when necessary.

The output directory contains:

- `adder.log`: unmodified stdout/stderr from Adder.
- `events/u1.jsonl` through `events/u5.jsonl`: actual `log` plugin event receipts.
- `filter-logs/u1.jsonl` through `filter-logs/u5.jsonl`: metadata-only configured
  and matched records for each customer.
- `summary.json`: rule counts, per-customer match and receipt counts, the shared
  block occurrence/hash, shutdown result, and the last observed router status.
- `status-last.json`: authenticated status before shutdown, including the
  router's exposed counters and delivery-outcome categories.
- `config/`, `credentials/`, `operator-token`, and `run.json`: generated inputs
  and the exact command. Keep this directory private and out of version control.

The live test does not wait for a rollback or rare event family. The independent
synthetic tests must exercise every rule, overlap, reload, and failure case.
Local log receipts prove that these events reached their local files; they do
not prove delivery through an external provider. Status is sampled before
shutdown and may lag the final event files. Reconciliation covers events observed
by Adder, not every event known to the source node. Restarting this stateless
process starts at the then-current tip and can lose intervening events.

## Dingo input

Run the same test against a running Dingo ChainSync listener, specifying its
network and address:

```sh
python3 scripts/test-managed-mainnet.py \
  --adder ./adder \
  --source-kind dingo \
  --source-address 127.0.0.1:3001 \
  --network mainnet \
  --output-dir /tmp/adder-managed-dingo-run
```

The script starts and stops only Adder. It neither starts nor changes the Dingo
node. A Dingo test on another network can use `--network preview` or the relevant
configured network; that run is recorded separately and does not satisfy the
public-mainnet criterion. `--source-address` may also override the public mainnet
peer while retaining `--source-kind public-mainnet`.
