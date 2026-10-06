# Troubleshooting Guide

This guide covers the most common operational issues encountered when running Adder, with concrete diagnostic commands, root causes, and resolutions.

---

## 1. Connection Issues

These issues occur when the `chainsync` input plugin cannot connect to or handshake with the Cardano node.

### A. Socket Path Problems (NtC)
* **Symptom**: Adder logs a connection refusal or "no such file or directory" error when attempting Node-to-Client (NtC) local socket connection.
* **Root Cause**: The Unix socket file does not exist at the specified path, or the user running Adder lacks read/write permissions on the socket.
* **Resolution**:
  1. Verify the Cardano node is fully running and has created the socket file.
  2. Double-check your socket path in your configuration or the `$CARDANO_NODE_SOCKET_PATH` env var.
  3. Check file permissions:
     ```bash
     ls -la /path/to/cardano-node.socket
     ```

### B. Network Selection Mismatch
* **Symptom**: Connection is established, but the protocol handshake fails immediately with errors like `handshake failed: network magic mismatch`.
* **Root Cause**: The network magic configured in Adder does not match the network magic of the running Cardano node (e.g. running Adder configured for `mainnet` against a node running on `preview`).
* **Resolution**: Align network configurations.
  - Set the correct network via env var:
    ```bash
    export INPUT_CHAINSYNC_NETWORK=preview
    ```
  - Or check network settings in `config.yaml`.

### C. NtC vs NtN Selection
* **Symptom**: Failure to connect or address resolution errors.
* **Root Cause**: Confusing Node-to-Client (local socket) with Node-to-Node (remote TCP/IP).
* **Resolution**:
  - For local node connections (NtC), ensure a socket path is provided.
  - For remote node connections (NtN), ensure a host and port (e.g., `relays-new.cardano-mainnet.iohk.io:3001`) are specified, and that TCP port 3001 is reachable:
    ```bash
    nc -zv relays-new.cardano-mainnet.iohk.io 3001
    ```

---

## 2. Configuration Issues

Configuration in Adder is layered: **explicit CLI flags override Environment variables, which override YAML file settings, which override Defaults**.

### A. Environment Variable Not Loading
* **Symptom**: Setting an env var has no effect on Adder.
* **Root Cause**: Typo in the prefix or suffix, or overriding the variable via an explicit CLI flag.
* **Resolution**:
  - Use unprefixed core names (`INPUT`, `OUTPUT`, `API_PORT`) or plugin namespaces (e.g., `INPUT_CHAINSYNC_`). There is no general `ADDER_` prefix.
  - List active variable names without printing credentials:
    ```bash
    env | cut -d= -f1 | grep -E "^(INPUT|OUTPUT|API|DEBUG|KUPO)(_|$)"
    ```

### B. YAML File Not Found or Invalid Value
* **Symptom**: "failed to read configuration file" or "yaml: unmarshal errors".
* **Root Cause**: Typo in file paths, malformed indentation, or type mismatch (e.g., passing a string where an integer is expected).
* **Resolution**: Validate your YAML syntax. You can test your yaml file format with:
  ```bash
  python3 -c "import yaml; yaml.safe_load(open('config.yaml'))"
  ```

---

## 3. Filter Issues

Filters use **AND** logic across pipeline stages. Within the Cardano filter, multiple address, pool, and DRep targets use **OR**; policy and asset constraints further narrow transaction matches. See [filtering](../README.md#filtering).

### A. Events Not Passing Filter
* **Symptom**: Adder runs but does not emit any events (the pipeline appears idle).
* **Root Cause**: Over-filtering (e.g. filtering for an asset policy ID on a block event where policy IDs do not apply) or malformed filter inputs.
* **Resolution**:
  - Check `--output-log-level`: `warn` and `error` suppress event records; use `info` or `debug` to see them.
  - Note that `--filter-policy` and `--filter-asset` do *not* apply to `input.block` or `input.governance` events.
  - Start with type-only filtering to verify raw event emission first:
    ```bash
    ./adder --filter-type input.transaction
    ```

### B. Formatting Requirements
* **Addresses**: Must be valid Bech32 payment or stake addresses (e.g., `addr1...`, `stake1...`).
* **Policy IDs**: Must be exactly 28-byte hex-encoded strings (56 characters).
* **DRep IDs**: Supports Bech32 (`drep1...` or `drep_script1...`) or raw hex hashes. Note that CIP-0129 DRep IDs with header bytes are resolved to their matching ledger hashes automatically.
* **Diagnostics**: Test validity of Bech32 addresses or hex hashes:
  ```bash
  # Check if policy ID is correct length
  echo -n "2dd15e0efd5c07b6bfbc0cf7fb2f767a50e189d7bfa50e1ef0b87abc" | wc -c
  ```

---

## 4. Push Notification Issues

### A. FCM Credentials
* **Symptom**: Push plugin logs `failed to get token` or `failed to read credential file`.
* **Root Cause**: The service account JSON file is missing, unreadable, or not a valid Google service account credential.
* **Resolution**: Ensure the service account file exists and is accessible:
  ```bash
  test -r /path/to/service-account.json
  ```

### B. Delivery Failures
* **Symptom**: "failed to send message to token..." logs appearing at ERROR.
* **Root Cause**: The FCM token has expired, is unregistered, or Firebase services are unreachable.
* **Resolution**: Inspect the error payload returned by FCM. If it indicates `UNREGISTERED`, remove the invalid token from your client registry via the `/v1/fcm` REST endpoint:
  ```bash
  curl -X DELETE http://localhost:8080/v1/fcm/your-expired-token
  ```

---

## 5. Webhook Issues

### A. Webhook Retries and Timeouts
* **Symptom**: Webhook messages are delayed or result in "webhook delivery failed after 3 retries, giving up".
* **Root Cause**: The target web server is slow, down, or returning non-2xx status codes.
* **Resolution**:
  - Test the target endpoint manually using curl to verify it is responsive and accepts POST payloads:
    ```bash
    curl -H "Content-Type: application/json" -X POST -d '{"type":"test"}' https://your-webhook-url.com
    ```
  - Retry configuration is available to Go callers through `webhook.WithRetryConfig`; there are no CLI retry/backoff flags. Select the output and endpoint with:
    ```bash
    ./adder --output webhook --output-webhook-url="https://your-webhook-url.com"
    ```

### B. TLS Certificate Problems
* **Symptom**: Webhook delivery fails with "x509: certificate signed by unknown authority".
* **Root Cause**: The target webhook server is using a self-signed or invalid TLS/SSL certificate.
* **Resolution**: Install CA certificates on your local machine, or secure a valid Let's Encrypt certificate for the webhook host.

---

## 6. Common Error Messages Reference

| Error String | Source Component | Root Cause | Resolution |
| :--- | :--- | :--- | :--- |
| `failed to read credential file: open ...: no such file or directory` | `output/push` | FCM credentials path in config is incorrect. | Verify path in config matches your local filesystem. |
| `failed to get token: oauth2: cannot fetch token` | `output/push` | Host has no internet connection, or Google IAM credentials are revoked. | Check network connectivity and service account status. |
| `invalid intersect point format: expected '<slot>.<hash>'` | `input/chainsync` | The point passed to `--input-chainsync-intersect-point` is malformed. | Pass the correct format: `<slot_integer>.<block_hex_hash>`. |
| `server returned status: 500` | `output/webhook` | The target server received the request but hit an internal server error. | Check logs on your webhook server to diagnose its crash. |
| `failed to parse credential file` | `output/push` / `internal/config` | JSON credential file contains syntax errors or invalid JSON format. | Validate service account credentials format. |
| `failed to resolve plugin config` | `internal/config` / `plugin` | Config keys contain unrecognized types or incompatible values. | Match types to option definitions in standard configs. |

---

## 7. Diagnosing Goroutine Leaks (Go 1.26+ Leak Profiling)

If Adder experiences a memory leak, it may be caused by orphaned goroutines stuck on unreachable primitives (like channels or mutexes).

The Go 1.26 `goroutineleakprofile` experiment detects goroutines blocked on unreachable synchronization objects. It cannot detect every worker that outlives its owner.

### Enabling the Leak Profiler
This feature **requires Go 1.26 or later**. It is disabled by default and requires compiling Adder with the `goroutineleakprofile` experiment enabled:
```bash
GOEXPERIMENT=goroutineleakprofile go build -o adder ./cmd/adder
```

> **Note**: If you attempt to compile with this flag on older Go versions, the compiler will fail with:
> `go: unknown GOEXPERIMENT goroutineleakprofile`

### Analyzing via HTTP pprof
When the profiler is enabled, you can access the `goroutineleak` profile via the net/http/pprof endpoint if the debug port is enabled:
```bash
go tool pprof http://localhost:6060/debug/pprof/goroutineleak
```

The ordinary `goroutine` profile lists all active goroutines. Use both profiles
when investigating workers that remain after shutdown; an empty leak profile
does not prove that every worker exited.
