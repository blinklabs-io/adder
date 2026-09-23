# Writing an Adder plugin

Start with the compiled [input](../examples/plugins/input.go) or
[output](../examples/plugins/output.go) example. Their
[tests](../examples/plugins/plugins_test.go) exercise restart and terminal
failure. They run without external services and use the public test package.

## Required contract

Every pipeline plugin and registered factory implements or returns
[ManagedPlugin](../plugin/plugin.go). It combines `Lifecycle` (`StartContext`,
`Stop`, `Failed`, and `Failure`) with role metadata, event ports, and diagnostic
errors. Add a compile-time assertion alongside the implementation:

```go
var _ plugin.ManagedPlugin = (*MyPlugin)(nil)
```

Embed `plugin.Base` by value to reuse channels, cancellation, worker tracking,
and failure reporting. Do not copy an instance after construction. Base is an
implementation, not an interface; a separate implementation may satisfy the
same contract. Pipeline and factory signatures require the complete interface;
there are no optional startup or failure capabilities. The interface checks
method availability, not correct cancellation behavior; tests and review must
establish the latter. An implementation without Base or a Start convenience
method is exercised in [pipeline tests](../pipeline/pipeline_test.go).

`Role` is stable and acquires no resources. An input requests only an output
port, an output only an input port, and a filter both. Unused ports return nil.
Built-ins offer a convenience `Start` delegating to
`StartContext(context.Background())`; custom plugins need not provide it.
When using Base, `StartContext` passes its context, port configuration, setup
function, and cleanup hooks to
`Base.StartRun`. Validate configuration before acquiring resources; acquire
run-specific connections/files in setup, not package initialization.

Configure a pipeline before starting it. Give it exclusive lifecycle ownership
of each connected instance. Never close a borrowed plugin channel, share an
instance across pipelines, or restart a connected plugin independently.
Reacquire channels after restart; old references do not switch to the new run.
When embedding directly, stop all raw `InputChan` producers before calling Stop.

## Workers, failures, and shutdown

Register workers using `Base.Go` during setup and capture run context/channels
once. StartContext must return with workers and ports ready. Produce
asynchronously; a synchronous send in setup can block before the pipeline
installs forwarding.
Do not call StartRun, Shutdown, or Wait from a tracked worker.

Choose error handling according to whether the worker can continue:

| Situation | Action |
| --- | --- |
| An event fails but the worker continues | Report with `SendError` or `TrySendError`; log dropped diagnostics as appropriate |
| A required worker cannot continue | Call `Fail(err)` and return |
| Run context canceled during normal shutdown | Return; do not manufacture a terminal error |
| Startup fails | Return the error; StartRun cleans up through the hooks |

After successful startup, `Failed()` must return a non-nil signal. Normal Stop
must not close it; restart creates a new signal. `Failure()` retains the first
terminal cause until the next run. These accessors must be concurrency-safe
and nonblocking. A plugin may return nil from `ErrorChan()` if it has no
recoverable diagnostics.

`Fail` retains the first cause and cancels the run. It does not synchronously
stop the plugin. Pipeline marks terminal failure unhealthy and cancels
forwarding; its owner must call Stop. The CLI does that and exits with an error.
Go callers should select on `Pipeline.Failed()` alongside their own shutdown
signal, read `Failure()`, and call `Stop()` on either path. Monitor recoverable
`ErrorChan` diagnostics independently. No automatic pipeline restart occurs.

Setup hooks must tolerate partially acquired resources. Use `BeforeWait` to
close connections or otherwise unblock workers. Use `AfterWait` to release
resources still needed by workers. Dependency-owned goroutines and callbacks
must also be stopped/joined before cleanup returns. An auxiliary worker may
finish normally; a required event loop must not silently die while its plugin
still appears healthy. Panics are programming faults; Base does not recover
and silently restart them.

Use the run context for dialing, HTTP requests, retry waits, and callback work.
Bound each external request with a child deadline and cancel it when finished.
Prefer timers/selects over uninterruptible sleeps. For custom writers or other
APIs without context support, require a bounded operation or a resource-close
hook that actually unblocks it. Do not wrap an uncooperative operation in a
throwaway goroutine and call that successful shutdown.

`DrainOnStop` closes input before joining a worker that ranges over it. It does
not make the whole pipeline lossless: forwarding has already stopped, upstream
buffers can be abandoned, and remote delivery may fail. Draining workers must
exit when input closes and their external operations must still be bounded.
There is no universal Stop deadline. Existing context-free embedded callbacks,
arbitrary writers, and draining output dependencies retain their own blocking
limits. The new context callback gives cooperative callers a cancellation path;
it cannot preempt arbitrary Go code. See
[callback tests](../output/embedded/embedded_test.go).

## Event ownership and delivery

An [Event](../event/event.go) and the data reachable from its Context and Payload
are immutable after publication. Fan-out shares referenced data with multiple
consumers. Producers must not reuse a published mutable buffer. Filters that
transform data copy every map, slice, or pointer-backed structure they change;
a shallow Event copy is insufficient. Consumers that need mutable working data
must make their own copy. See the
[executable transformation example](../examples/plugins/ownership_test.go).

There is no generic deep-copy step or implicit JSON conversion. Concrete payload
types survive the pipeline. Document custom event types and their payload
schema, including compatible schema evolution. Chainsync rollbacks are separate
events; block-only consumers do not see them and cannot assume finality.

Inputs can run with zero outputs and no observer: events are discarded at the
terminal loop. Configured outputs apply blocking backpressure in registration
order. API observation is best effort. Durable storage, acknowledgements,
exactly-once processing, and lossless shutdown are not provided by Base.

## Registration and configuration

Go embedders can construct a plugin and add it directly to a pipeline. CLI
plugins also register a `PluginEntry` in package `init` and are blank-imported
by [input.go](../input/input.go), [filter.go](../filter/filter.go), or
[output.go](../output/output.go). Register metadata/factories only; no network
connections or worker startup during initialization. The examples intentionally
do not register globally.

Declare scalar defaults in `PluginOption` and register a factory with signature
`func(plugin.Options) (plugin.ManagedPlugin, error)`. Read values with `String`,
`Bool`, `Int`, or `Uint` using the exact declared name and type. The registry validates
scalar types before invocation; a mismatched accessor is a programmer error.
Factories validate plugin-specific semantics and return errors without logging
and returning nil. Do not start workers or contact remote services in a factory.
Local configuration-file validation is allowed. See
[chainsync's factory](../input/chainsync/plugin.go) and
[Telegram startup](../output/telegram/telegram.go).

Go callers use `plugin.GetPlugin(kind, name, map[string]any{...})` or
`PluginEntry.New` for defaults plus explicit values. The CLI uses
`ResolveConfig` followed by `Configuration.New`; this snapshots
**explicit CLI > environment > YAML > defaults**. Custom environment aliases
beat generated names at the environment tier. Definitions contain no mutable
option destinations, and configurations do not affect other instances.
Required settings are checked for selected plugins at construction.
See [architecture](architecture.md#3-configuration-precedence) and the
[breaking migration guide](plugin-configuration-migration.md).

## Required tests for a new plugin

Use [plugintest.Lifecycle](../plugintest/contract.go) with a fixture that needs
no real external service. This helper also requires `Running`, `Done`, and
`Context` diagnostics, as provided by Base. A custom implementation without
those methods needs equivalent behavioral tests; the pipeline does not require
the diagnostics. It checks concurrent duplicate start/stop, restart,
port shape, run-specific channels, context cancellation, and no spurious
terminal failure during normal shutdown. `plugintest.FailedStart` checks cleanup
of a startup error. Supply plugin-specific tests for acquired resources; a
successful generic contract test alone cannot prove that a socket/file closed.

Also cover blocked startup cancellation, blocked delivery cancellation, failure
with no error reader, recoverable versus terminal errors, and partial-resource
cleanup as applicable. Test special single-use configurations separately; for
example, embedded forwarding closes the caller-provided channel and cannot
restart with that same channel. Validate configuration and factory mappings.
Run `go test -race` on the plugin and its integration path. Avoid requiring
mainnet, external credentials, desktop notifications, or host services in CI.

The public helpers live outside `internal` so an external Go module can import
them. The repository-specific Cardano node fixture remains internal; it is not
part of the plugin author's supported test API.
