# Local CLI integration tests

From the repository root, run:

```sh
./scripts/test-integration.sh
```

Requires Bash, the Go toolchain specified in `go.mod`, and a C compiler for Go's
race detector. Supported on macOS and Linux. Go may download dependencies during
the build; the test scenarios themselves use loopback connections only and need
no node, API key, or other service.

The script builds the real `cmd/adder` binary with `-race`, then runs
the suites in [`tests/integration`](../../tests/integration)
with the `localintegration` build tag. Each scenario starts a fresh CLI process
against a local HTTP/2 UTxO RPC fixture. The production input plugin, filters,
pipeline, log output plugin, configuration parsing, health API, and SSE API run
unchanged. Child processes receive an isolated environment so exported Adder
settings cannot change the test configuration.

## Scope and additional coverage

Add an integration case only when it can detect a failure across the real CLI,
plugin, transport, filesystem, or API boundaries that existing unit tests do not
exercise. Keep argument validation, formatting edge cases, and severity truth
tables in their unit suites. A new configuration permutation alone is not a
reason to launch another process.

| Case | Behavior checked |
| --- | --- |
| JSON output at info | The single log threshold enables both event output and application diagnostics through the real CLI |
| Text output at info | The CLI selects the text writer and delivers the four event types through the complete pipeline |
| File output at debug | The CLI opens the configured file, routes events there, and leaves stdout empty |
| Output threshold error | The log plugin consumes events without writing and suppresses routine diagnostics, while SSE delivery remains active |
| Environment over YAML; CLI over both | Resolved application and plugin settings, including the output threshold, reach the running output: actual JSON file contents and stderr reflect the winning settings, and losing paths are not created |
| Transaction/rollback filter | The CLI-wired filter applies to both log output and the API observer; BLOCK and GOVERNANCE reach neither |
| Missing output directory | Output startup failure terminates the CLI without consuming input; its API port can be rebound after exit |
| Occupied API port | A real bind failure exits nonzero before input consumption |
| Invalid threshold without log output | `--output webhook` still validates the shared log threshold before startup, even though the output factory is not called |
| Terminal RPC failure at error level | A transport error reaches the managed plugin failure path and terminates the real CLI with a visible error |
| Transient RPC failure | The input reconnects once, events resume without duplicates in the fixture batch, the API reports healthy, and signal shutdown completes |

Both the CLI and harness use the race detector.
The script exits nonzero if the build or tests fail. Successful streaming cases
leave SSE connected through signal shutdown and require exit within five seconds;
the logging cases exercise both SIGINT and SIGTERM. These tests configure startup
settings; they do not test live configuration reloads.

The fixture supplies BLOCK, TX, GOVERNANCE, and ROLLBACK events and holds its
stream open until shutdown. Enabled-output cases wait for log delivery before
signaling: pipeline shutdown does not promise lossless delivery of every
in-flight event. The errors-only case checks delivery through SSE.

## Reruns and artifacts

Tests are split by failure area. `fixture_test.go` supplies the RPC server and
`process_test.go` owns process startup, cleanup, health, SSE, and retained logs.

| File | Focused `-run` pattern |
| --- | --- |
| [`logging_test.go`](../../tests/integration/logging_test.go) | `TestLoggingIntegration` or `TestOutputLevelIntegration` |
| [`configuration_test.go`](../../tests/integration/configuration_test.go) | `TestConfigurationIntegration` |
| [`filtering_test.go`](../../tests/integration/filtering_test.go) | `TestFilteringIntegration` |
| [`startup_test.go`](../../tests/integration/startup_test.go) | `TestStartupIntegration` |
| [`failure_test.go`](../../tests/integration/failure_test.go) | `TestTerminalErrorIntegration` |
| [`reconnect_test.go`](../../tests/integration/reconnect_test.go) | `TestReconnectIntegration` |

Additional arguments are passed to `go test`. Use `-run` to select cases while
still compiling the shared helpers:

```sh
./scripts/test-integration.sh -run TestOutputLevelIntegration -count=3
./scripts/test-integration.sh -run TestTerminalErrorIntegration
./scripts/test-integration.sh -run TestConfigurationIntegration
./scripts/test-integration.sh -run TestFilteringIntegration
./scripts/test-integration.sh -run TestStartupIntegration/api-port-occupied
./scripts/test-integration.sh -run TestReconnectIntegration
```

The script prints a new temporary artifact directory and keeps it after both
success and failure. It contains the tested `adder` executable, `tests.log`, and
per-scenario `stdout.log`, `stderr.log`, and `invocation.json` with the exact CLI
arguments and test environment overrides. Configuration scenarios also retain
their `config.yaml` when used. File-output cases retain the selected JSON log file.
Delete that directory when no longer needed. Processes are killed and reaped on
test assertion failures; normal cases explicitly verify graceful shutdown.

The suite uses synthetic protobuf Cardano data. It does not test ChainSync
handshakes, mainnet availability, CBOR decoding, prolonged retry/backoff behavior, every governance
certificate, or external output services. Normal `go test ./...` excludes this
suite; use the script explicitly for the process-level checks.
