# Architecture & Concurrency Model

This document describes the high-level architecture, plugin system, configuration precedence, and concurrency model of Adder.

---

## 1. High-Level Overview

Adder is built around a concurrent **Pipeline-Plugin** architecture. Data flows from **Inputs**, through a sequence of **Filters** (each running in its own goroutine), into **Outputs**. Every stage is connected by Go channels, and every plugin runs concurrently with its neighbors.

```text
+--------------------+
|    Input Plugin    |   OutputChan()
| chainsync/mempool/ | -------------.
|      utxorpc       |              |
+--------------------+              |  chanCopyLoop
         |                          v
         | ErrorChan()      +---------------+
         |                  | p.filterChan  |
         |                  +---------------+
         |                          |  chanCopyLoop
         |                          v
         |                  +---------------+
         |                  | Filter chain  |   each filter runs its own
         |                  |   cardano ->  |   goroutine; chanCopyLoop
         |                  |     event     |   bridges filter[i] -> [i+1]
         |                  +---------------+
         |                          |  chanCopyLoop
         |                          v
         |                  +---------------+
         |                  | p.outputChan  |
         |                  +---------------+
         |                          |  outputChanLoop
         |                          v
         |                  +---------------+
         |                  | Output Plugin |
         |                  |  log/webhook/ |
         |                  | telegram/push |
         |                  |    /notify    |
         |                  +---------------+
         |  errorChanWait (one goroutine per plugin)
         '-----------------> p.errorChan --> logged by main.go
```

### Topology

`Pipeline` stores inputs, filters, and outputs as slices (`pipeline/pipeline.go:31-33`) and `AddInput` / `AddFilter` / `AddOutput` append to them, so the pipeline package itself supports M inputs, N filters, and P outputs.

The `adder` CLI uses a narrower subset of that capability (`cmd/adder/main.go:190-214`):
- **Exactly one input**: `plugin.GetPlugin(plugin.PluginTypeInput, cfg.Input)` — the single plugin named by `--input` / `INPUT` (`chainsync`, `mempool`, or `utxorpc`). There is no CLI flag for adding a second input.
- **Every registered filter (N)**: `main.go` loops over `plugin.GetPlugins(plugin.PluginTypeFilter)` and adds all of them — currently `cardano` and `event` (`filter/filter.go`). They are chained in registration order; each one's output feeds the next.
- **Exactly one output**: the single plugin named by `--output` / `OUTPUT` (`log`, `webhook`, `telegram`, `push`, or `notify` — see `output/output.go`).

Embedding the `pipeline` package directly in your own Go program is what unlocks the multi-input / multi-output form.

---

## 2. The Plugin System

All inputs, filters, and outputs implement the unified `Plugin` interface defined in `plugin/plugin.go`:

```go
type Plugin interface {
	Start() error
	Stop() error
	ErrorChan() <-chan error
	InputChan() chan<- event.Event
	OutputChan() <-chan event.Event
}
```

### Plugin Lifecycle States

```text
+---------------+         Start()         +---------------+
|  Constructed  | ----------------------> |    Running    |
|   (Stopped)   | <---------------------- | (Worker Loop) |
+---------------+          Stop()         +---------------+
```

`Start()` is where a plugin creates its channels and spawns its worker
goroutine; `Stop()` signals that goroutine to exit. The pipeline itself is
restartable — `Pipeline.Start` detects a closed `doneChan` and recreates
`doneChan`, `filterChan`, `outputChan`, `errorChan`, and `stopOnce`
(`pipeline/pipeline.go:94-103`). Because the old channels are closed, callers
must re-obtain the error channel via `ErrorChan()` after a restart.

### Auto-Registration Mechanism
Adder uses Go's package initialization mechanism for auto-registering plugins.
1. Each plugin package registers itself in an `init()` block calling `plugin.Register()`.
2. The main command-line interfaces import these plugins blankly (e.g., `_ "github.com/blinklabs-io/adder/input/chainsync"`) in central registration files:
   - `input/input.go`
   - `filter/filter.go`
   - `output/output.go`
3. `plugin.Register` appends the `PluginEntry` to a package-level slice (`plugin/register.go:54-56`). `main.go` later looks an entry up by type and name and calls its `NewFromOptionsFunc` to construct the instance (`plugin/register.go:113-123`).

---

## 3. Configuration Precedence

Adder loads configuration in two passes with different precedence in each, driven by `cmd/adder/main.go`.

**Core config** (`Input`, `Output`, `Api`, `Logging`, etc.) is resolved by a single call to `cfg.LoadWithFlags` (`internal/config/config.go:171-211`). Internally it runs in the *opposite* order to the precedence it produces: it first snapshots the flags the user actually set (`fs.Visit`), then applies unprefixed environment variables via `envconfig`, then unmarshals the YAML file over the top, then re-applies the snapshotted CLI flags last. The net precedence is therefore CLI flags, then YAML, then environment variables (e.g. `INPUT`, `OUTPUT`, `API_ADDRESS`, `LOGGING_LEVEL` — see the `envconfig` tags in `internal/config/config.go`), then internal defaults:

```text
+-----------------------+
|  Command Line Flags   |  (Highest Precedence)
+-----------------------+
           |
           v
+-----------------------+
|  YAML Config File     |  (via config.yaml)
+-----------------------+
           |
           v
+-----------------------+
| Environment Variables |  (via envconfig: INPUT, OUTPUT, API_ADDRESS, ...)
+-----------------------+
           |
           v
+-----------------------+
|   Internal Defaults   |  (Lowest Precedence)
+-----------------------+
```

**Plugin options** (each `PluginOption.Dest`, bound to a CLI flag in `AddToFlagSet`, `plugin/option.go:45-88`) go through the opposite order in `main.go`: `cfg.LoadWithFlags` (:125) applies CLI flags first, then `plugin.ProcessConfig` (:130) applies YAML, then `plugin.ProcessEnvVars` (:135) applies environment variables — each pass unconditionally overwrites the same `Dest` pointer, so the last one run wins:

```text
CLI Flags  -->  YAML Config  -->  Environment Variables   (each overwrites the previous)
(lowest)                          (highest, applied last)
```

So effective precedence is **environment > YAML > CLI > default** for plugin options, the reverse of the core-config case above. Plugin environment variables are named `<TYPE>_<NAME>_<OPTION>` (e.g. `INPUT_CHAINSYNC_ADDRESS`, built from `plugin/register.go:69-83` and `plugin/option.go:90-104`). An option may also declare a `CustomEnvVar`, which is checked *in addition to* the generated name and, being second in the list, wins if both are set (`plugin/option.go:106-109`) — for example `CARDANO_NETWORK` and `CARDANO_NODE_SOCKET_PATH` on the `chainsync` input (`input/chainsync/plugin.go:52,74`). Similarly, `CustomFlag` replaces the generated flag name with `<type>-<customflag>` (`plugin/option.go:51-55`), which is how `--filter-address` gets its short form.

---

## 4. Concurrency & Goroutine Model

Adder uses Go channels for event and error flow between plugins, and a small set of mutexes (`lifecycleMu`, `runningMu`) plus `sync.Once` (`stopOnce`) in `Pipeline` to coordinate lifecycle transitions (`Start`/`Stop`) safely.

### Active Goroutines

When Adder is fully running, the following goroutines are active:

1. **Main Goroutine**: Manages initial configuration, parses CLI arguments, builds/starts the pipeline, and listens for OS signals (`SIGINT`, `SIGTERM`) to trigger graceful shutdown.
2. **Input Goroutines**: Owned by the active input plugin. For `chainsync`, the protocol goroutines belong to the gouroboros connection created in `setupConnection` (`input/chainsync/chainsync.go:283-291`); Adder supplies `handleRollForward` / `handleRollBackward` callbacks that decode blocks and transactions and push events onto the plugin's own buffered channel (capacity 2048, `chainsync.go:162-167`).
3. **Pipeline Goroutines**: Spawned by `Pipeline.Start` (`pipeline/pipeline.go`), one per input/filter/output link:
   - **`chanCopyLoop`**: One goroutine per input->filter, filter->filter, filter->output, or (when there are no filters) input->output hop, bridging events asynchronously between stages.
   - **`outputChanLoop`**: A single goroutine that reads matched events from the output channel and dispatches each event to every registered output plugin's input channel **sequentially, in a blocking loop** — a slow or blocked output delays delivery to every output after it. After the fan-out it also does a **non-blocking** send to the registered observer channel, dropping the event if the observer is full (`pipeline/pipeline.go:279-289`); that observer is the API's `EventHub`.
   - **`errorChanWait`**: One goroutine per active plugin, listening on that plugin's error channel and forwarding errors to the pipeline's central error channel. `main.go:250-256` runs a further goroutine that ranges over `pipe.ErrorChan()` and logs errors without exiting.
4. **Filter Goroutines**: Each filter's `Start()` creates buffered input and output channels (capacity 10) and spawns one worker goroutine that reads an event, applies its match logic, and forwards matches downstream — `filter/cardano/cardano.go:45-54` and `filter/event/event.go:46-58`.
5. **Output Goroutines**: Each output plugin's `Start()` likewise spawns one worker goroutine reading from a buffered event channel (capacity 10) — `log`, `webhook`, `telegram`, `notify`, and `push` all follow this shape. The `webhook` and `telegram` plugins additionally implement delivery retries with backoff (`output/webhook/webhook.go:524-` `sendWebhookWithRetry`, `output/telegram/telegram.go:562-` `sendMessageWithRetry`); the others do not.
6. **API Server Goroutine**: `api.Start` binds the listener and then calls `server.Serve` in a background goroutine (`api/api.go:187-193`). `GET /events` is registered on the bare mux with no group prefix (`cmd/adder/main.go:221`) and serves WebSocket when the client requests an upgrade, otherwise SSE (`api/events.go:194-208`). The `/fcm` and `/qrcode` routes exist only when the `push` output is selected, and they sit under the `/v1` base path (`output/push/api_routes.go:23-38`).

### Graceful Coordination & Shutdown

Graceful shutdown is owned by `Pipeline.Stop()` (`pipeline/pipeline.go:189-234`):
- **`Pipeline.stopOnce` (`sync.Once`)**: Ensures the shutdown body below runs exactly once, even if `Stop()` is called more than once concurrently.
- **`Pipeline.doneChan chan bool`**: Closed first, signaling every `chanCopyLoop`, `outputChanLoop`, and `errorChanWait` goroutine to exit.
- **`Pipeline.wg` (`sync.WaitGroup`)**: `Stop()` waits on it immediately after closing `doneChan`, so all pipeline goroutines have exited before the next step.
- **Sequential plugin `Stop()` calls**: Only after `wg.Wait()` returns does `Stop()` call `Stop()` on each input, then each filter, then each output, collecting any errors.
- **Shared channel closure**: Once all plugins have been stopped, `Stop()` closes `p.errorChan`, `p.filterChan`, and `p.outputChan`.

Note that `sync.Once` for shutdown safety is used by `Pipeline` and by some plugins (`input/mempool`, `input/utxorpc`, `filter/cardano`, `filter/event`), but not by every plugin — `input/chainsync` and the output plugins (`webhook`, `telegram`, `notify`, `push`) instead rely on channel-closed checks (and, in `webhook`'s case, a mutex) around their own shutdown paths.

```text
Pipeline.Stop() called
               |
               v
        stopOnce.Do(...)
               |
               v
      Close p.doneChan
               |
               v
   p.wg.Wait() (pipeline goroutines exit:
   chanCopyLoop, outputChanLoop, errorChanWait)
               |
               v
   Stop() each input, then each filter,
   then each output (errors collected)
               |
               v
   Close p.errorChan, p.filterChan, p.outputChan
               |
               v
          Safe Shutdown
```

---

## 5. Adder Tray Application Architecture

The `adder-tray` system tray application (`cmd/adder-tray/` and `tray/`) does **not** run or wrap the core pipeline itself — no code under `tray/` or `cmd/adder-tray/` imports the `pipeline` package. As the doc comment on `ConnectionManager` (`tray/connection.go:25-27`) puts it, "the tray no longer manages adder as a subprocess but connects to it as an API client". `adder` runs as a separate per-user background process and the tray talks to it over a WebSocket to `GET /events` (`tray/events.go:235-236`, gorilla `websocket.DefaultDialer`); the SSE path in `api/events.go` is the server's fallback for clients that do not request an upgrade, not something the tray uses.

### Key Components

- **Fyne GUI Engine**: `cmd/adder-tray/main.go` creates the Fyne app; `tray/app.go:512` type-asserts it to `desktop.App` and drives the tray menu and icon through `SetSystemTrayMenu` / `SetSystemTrayIcon` (`app.go:623`, `:681`, `:744`), swapping the icon as connection status changes.
- **Setup Wizard (`tray/wizard/`)**: A step-by-step Fyne flow that assists the user in generating configuration. `SetupRunner.Apply` (`tray/setup/runner.go:82-186`) persists the plan as two separate files: the engine's `config.yaml` (read by the `adder` background process) and the tray's own `adder-tray.yaml` (`tray/config.go:28`). The tray file holds API address/port, autostart, notification prefs, the notification filter, and the rate-limit settings — see the `TrayConfig` struct at `tray/setup/store.go:31-49`. The filter deliberately lives in the tray config rather than the engine config so the tray's notification engine owns target matching (`runner.go:101-103`).
- **Rules Engine & Rule Derivation (`tray/notifications/`)**:
  - `RulesFromPlan` (`tray/notifications/rules.go:243`) translates a `setup.SetupPlan` into concrete `Rule` values, with per-target helpers such as `walletRules`, `drepRules`, `poolRules`, `assetRules`, and `policyRules`.
  - The engine rate-limits notifications: matches beyond `NotifyRateLimit` within `NotifyRateWindow` are coalesced into one batched request emitted at the window boundary, and a non-positive limit disables coalescing (`tray/notifications/engine.go:169-185`). Defaults are 1 notification per 5 seconds (`tray/setup/store.go:53-56`).
- **System Service Integration (`tray/setup/`)**:
  - `SetupRunner.Apply` calls `Service.EnsureRegistered` then `Service.RestartIfConfigChanged` and afterwards points the tray's API connection at the configured address/port (`runner.go:124-152`). Both service failures are *soft*: the config is still persisted and surfaced via `ApplyResult`.
  - The `ServiceManager` implementation is per-platform: a launchd LaunchAgent plist driven by `launchctl` on macOS (`service_darwin.go`), a systemd **user** unit on Linux (`service_linux.go`), and on Windows an HKCU `...\CurrentVersion\Run` value that autostarts the *tray*, with the tray launching `adder.exe` itself as a detached, windowless child tracked by PID (`service_windows.go:19-38`). Windows deliberately avoids Task Scheduler and Windows Services so nothing requires elevation. FreeBSD is stubbed out and returns "FreeBSD service management is not implemented" (`service_freebsd.go`).
  - `App.Shutdown()` (`tray/app.go:855-869`) closes the tray's quit channel, disconnects its API connection, stops the notification engine, and quits the Fyne app — it does not stop the `adder` process, which keeps running independently.

### Architectural Layout

```text
+--------------------------------------------------------------------------+
|                           adder-tray App (GUI)                           |
|                     Fyne UI / Wizard / Rules Editor                      |
+--------------------------------------------------------------------------+
           |                         |                         |
         writes                  registers                  connects
           v                         v                         v
+----------------------+  +----------------------+  +----------------------+
|     config.yaml      |  |  OS Service Manager  |  |    adder process     |
|   (engine config)    |  | (launchctl / systemd |  |  (separate per-user  |
|                      |  |   / HKCU Run key +   |  | process, own binary) |
|   adder-tray.yaml    |  |   detached child)    |  +----------------------+
|(tray + notify prefs) |  +----------------------+
+----------------------+
```

The "OS Service Manager" box registers and restarts `adder` through the
platform backend behind `ServiceManager` (`SetupRunner.Apply`,
`tray/setup/runner.go:124-148`). On macOS and Linux the OS supervises the
process and the tray never holds a handle to it; on Windows there is no
service supervisor, so the tray starts `adder.exe` as a detached child and
tracks it by PID (`tray/setup/service_windows.go:19-38`). Either way the
pipeline runs in the `adder` process, not in the tray. `adder` reads only
`config.yaml` and runs its own `Input -> Filter -> Output` pipeline and API
server. The tray's rule derivation (`tray/notifications/rules.go`) turns a
`SetupPlan` into desktop-notification rules locally — those rules are never
sent to the `adder` process.
