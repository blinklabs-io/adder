# Architecture & Concurrency Model

This document describes the high-level architecture, plugin system, configuration precedence, and concurrency model of Adder.

---

## 1. High-Level Overview

Adder is built around a concurrent **Pipeline-Plugin** architecture. Data flows from one or more blockchain **Inputs**, through a sequence of **Filters** (each decoupled as concurrent processes), and is dispatched asynchronously to multiple **Outputs**.

```text
+--------------------+      Go Channel (event.Event)     +--------------------+
|   Input Plugins    | --------------------------------> |                    |
|  (chainsync, etc.) | <-------------------------------- |   Pipeline Core    |
| (One or More, M)   |      Go Channel (error)           |   (pipeline.go)    |
+--------------------+                                   +--------------------+
                                                           |        |
                                        Filter Input       |        | Dispatch Event
                                        (Asynchronous)     v        v
                                                      +------------+
                                                      |  Filters   |
                                                      | (cardano/  |
                                                      |  event, N) |
                                                      +------------+
                                                            |
                                                            | Matched Event
                                                            v
                                                      +------------+
                                                      |  Outputs   |
                                                      |  Plugins   |
                                                      | (webhook,  |
                                                      |  P outputs)|
                                                      +------------+
```

### The M:N:P Topology Rule
Adder is designed with a highly flexible **M:N:P topology** coordinated by the central `pipeline/pipeline.go` engine:
- **One or More Inputs (M)**: Reads data concurrently from different block-producing sources or protocol drivers (e.g., combining `chainsync` and `mempool` inputs).
- **Zero or More Filters (N)**: A sequential pipeline of filter plugins (e.g., transaction addresses, asset policies, pool IDs, DReps) connected asynchronously via background copy loops.
- **One or More Outputs (P)**: Asynchronously dispatches matching blockchain events concurrently to their target endpoints (e.g., stdout, webhooks, Telegram, Push notifications).

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

### Auto-Registration Mechanism
Adder uses Go's package initialization mechanism for auto-registering plugins.
1. Each plugin package registers itself in an `init()` block calling `plugin.Register()`.
2. The main command-line interfaces import these plugins blankly (e.g., `_ "github.com/blinklabs-io/adder/input/chainsync"`) in central registration files:
   - `input/input.go`
   - `filter/filter.go`
   - `output/output.go`

---

## 3. Configuration Precedence

Adder configuration is layered to allow flexible overrides. Command-line flags always take the highest precedence, followed by YAML configuration files, followed by Environment variables, and finally falling back to Internal Defaults.

```text
+-----------------------+
|  Command Line Flags   |  (Highest Precedence - via cobra/pflag)
+-----------------------+
           |
           v
+-----------------------+
|  YAML Config File     |  (via config.yaml)
+-----------------------+
           |
           v
+-----------------------+
| Environment Variables |  (via envconfig, prefixed with ADDER_ / plugin names)
+-----------------------+
           |
           v
+-----------------------+
|   Internal Defaults   |  (Lowest Precedence)
+-----------------------+
```

---

## 4. Concurrency & Goroutine Model

Adder is highly concurrent and relies on asynchronous message passing over Go channels rather than shared memory locks.

### Active Goroutines

When Adder is fully running, the following goroutines are active:

1. **Main Thread**: Manages initial configuration, parses CLI arguments, builds/starts the pipeline, and listens for OS signals (`SIGINT`, `SIGTERM`) to trigger graceful shutdown.
2. **Input Goroutines**: Run by each active input plugin (e.g., gouroboros `ChainSync` driver). They block on socket reads from the Cardano node, decode blocks/transactions, and write them to their individual output channels.
3. **Pipeline Orchestrator Goroutines**: Run by `pipeline/pipeline.go`. The orchestrator starts:
   - **`chanCopyLoop`**: Multiple concurrent background copying loops that safely bridge events asynchronously between sequential filters (and between input and filters).
   - **`outputChanLoop`**: Reads matched events from the final output channel and distributes them concurrently to each registered output plugin's input channel.
   - **`errorChanWait`**: Listens on each active plugin's error channel and forwards errors to the pipeline's central error channel.
4. **Output Goroutines**: Each active output plugin (e.g. Webhook, Telegram, Push) spawns its own background worker goroutine to process and deliver events asynchronously, managing its own network requests and retries in the background.
5. **API / Tray Server Goroutines** (Optional): Spun up by the API or system tray app to serve WebSocket / SSE event streams and REST endpoints (e.g., `/events` or `/fcm`).

### Graceful Coordination & Shutdown

Graceful coordination is owned and orchestrated by the central **Pipeline Core** via channel signals and sync primitives:
- **`doneChan chan bool`**: Managed and closed by the **Pipeline** inside its `Stop()` method to signal the orchestrator loops (`chanCopyLoop`, `outputChanLoop`, `errorChanWait`) to exit.
- **Component Lifecycle Ownership**: The pipeline sequentially calls `Stop()` on all of its registered inputs, filters, and outputs. Each plugin is then individually responsible for gracefully terminating its own background workers (closing its own internal channels and waiting on its internal `sync.WaitGroup`) before returning.
- **`sync.Once`**: Leveraged throughout the pipeline and individual plugins to ensure `Stop()` teardown logic is executed exactly once, preventing concurrent channel closure panics or data races.

```text
User Signal / p.Stop() Called
               |
               v
     Close central doneChan
               |
     +---------+---------+
     v                   v
Terminate Copy Loops     Sequentially Stop Plugins
(chanCopyLoop, etc.)     (p.inputs -> p.filters -> p.outputs)
                         Each plugin:
                         1. Closes internal channels
                         2. Exits background workers
                         3. Waits on WaitGroup
                                 |
                                 v
                          Safe Shutdown
```

---

## 5. Adder Tray Application Architecture

The `adder-tray` system tray application (`cmd/adder-tray/` and `tray/`) wraps the core pipeline and integrates it with a cross-platform desktop GUI.

### Key Components

- **Fyne GUI Engine**: Drives the native OS window rendering, tray icon lifecycle, and interactive onboarding wizard.
- **Setup Wizard (`tray/wizard/`)**: A step-by-step GUI flow that assists the user in generating the canonical `adder.yaml` configuration file. It configures the network, monitors specific targets (wallets, pools, DReps), sets up notification destinations, and maps notification preferences.
- **Rules Engine & Rule Derivation (`tray/notifications/`)**:
  - Automatically translates high-level user preferences (`SetupPlan`) into concrete `Rule` configurations.
  - Leverages **Notification Coalescing / Rate Limiting** to batch burst events (such as numerous transactions in a short window) into single consolidated desktop alerts, preventing system notification spam.
- **System Service Integration (`tray/setup/service.go`)**:
  - Dynamically registers and manages `adder` as a background OS service (using macOS LaunchAgents, systemd on Linux, or Windows Services).
  - This allows the tray app to start, stop, restart, and monitor the background tailing worker seamlessly.

### Architectural Layout

```text
+---------------------------------------------------------+
|                  adder-tray App (GUI)                   |
|  - Fyne UI / Onboarding Wizard / Rules Editor           |
+---------------------------------------------------------+
       |                                      |
       | Reads/Writes                         | Controls (Start/Stop)
       v                                      v
+-------------------------+            +--------------------+
|  SetupPlan (adder.yaml) |            |  OS Background     |
|  Notification Prefs     |            |  Service Manager   |
+-------------------------+            | (Launchctl/systemd)|
       |                               +--------------------+
       | Translates to Rules                    |
       v                                        v
+---------------------------------------------------------+
|                adder Background Worker                  |
|  - Reads setup plan and derives pipeline rules          |
|  - Runs the Input -> Filter -> Output Pipeline          |
+---------------------------------------------------------+
```
