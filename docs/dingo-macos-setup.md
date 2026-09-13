# Dingo QA Manual

This manual outlines the concise setup and verification steps for testing the
Adder event-streaming pipeline against the Dingo Go Cardano node on the
`preview` network.

---

## 1. Local Orchestration Setup (Docker & Compose)

We utilize standard Docker Compose to run both services on any platform (Linux,
Windows, macOS). The stack is configured with **disk-safe log rotation**
limiting the logs for each container to a maximum of 10MB (5MB per file with
at most 2 files kept) to prevent log growth from exhausting host disk space.

### Start the Stack

Navigate to the repository worktree and start the containers in the background:

```bash
docker compose up --build -d
```

---

## 2. Real-Time Verification Checks (Docker)

### Step A: Verify Connection and Intersect

By default, the `config-preview.yaml` configuration is configured with `intersect-tip: true`. This instructs Adder to perform a ChainSync intersection at the current tip of the Dingo node instead of starting from Genesis (slot 0).

1. **First-Time Sync (Genesis Tip):** On a brand new, empty stack where Dingo starts with a clean database volume, the node's initial tip is Genesis (slot 0). Thus, the initial intersection occurs at genesis, and Adder begins streaming blocks sequentially starting from block 1.

Verify that Dingo has booted, Adder successfully connected to the UNIX domain socket, completed the intersection handshake, and is processing blocks:

```bash
docker compose logs adder | tail -n 20
```

**Expected Output:**

You should see incoming block notifications with incrementing slot numbers:

```text
adder-1  | 2026-08-29 05:02:49 BLOCK        slot=20         block=1        hash=cd619529...
adder-1  | 2026-08-29 05:02:49 BLOCK        slot=40         block=2        hash=819b76c8...
```

2. **Verifying ChainSync Intersection (Active Tip):** To explicitly exercise and verify the intersection logic with a non-genesis tip, allow the stack to run and sync some blocks (e.g., until slot 100 or higher). Then, restart only the `adder` service:

```bash
docker compose restart adder
```

Now, check the logs again:

```bash
docker compose logs adder | tail -n 20
```

**Expected Output:**

Rather than restarting from block 1 (slot 20), Adder query-intersects at the node's active synced tip, and immediately resumes streaming blocks from that point onward (e.g. starting at slot 120 or whatever the last synced block was):

```text
adder-1  | 2026-08-29 05:03:15 BLOCK        slot=120        block=6        hash=7d77b8f2...
adder-1  | 2026-08-29 05:03:17 BLOCK        slot=140        block=7        hash=1a2b3c4d...
```

### Step B: The "Something Weird" Check (On-Demand Grep)

At any point, execute this single-line command to instantly filter out normal
operational output and identify anomalies (such as deserialization failures,
network disconnect loops, or restarts):

```bash
docker compose logs | grep -iE "error|panic|warn|reconnect"
```

---

## 3. Teardown and Cleanup (Docker)

When testing is complete, stop the containers and fully destroy all associated
volumes (leaving the host clean):

```bash
docker compose down -v
```

---

## 4. High-Performance macOS Alternative (Without Docker Desktop)

For developers on macOS (especially Apple Silicon) seeking a lightweight,
high-performance alternative to Docker Desktop, you can utilize **Apple's
native open-source container runtime**:
👉 **[apple/container](https://github.com/apple/container)**

This tool is written in Swift, utilizes the macOS native
`Virtualization.framework`, and executes Linux OCI container images directly as
lightweight virtual machines with near-zero filesystem I/O overhead.

### Why this is a Breakthrough (Host UNIX Socket Relay)

Unlike Docker Desktop (which cannot reliably bridge active UNIX domain sockets
between the macOS host and Linux VM), Apple's native `container` provides a
dedicated **`--publish-socket` relay mechanism**.

Dingo creates `/ipc/node.socket` inside the container, and Apple Container's
hypervisor socket relay dynamically exposes the host socket at
`~/dingo-ipc/node.socket`. This provides complete, bidirectional UNIX socket
communication without relying on file-sharing mounts (like VirtioFS, which do not
support active, guest-created UNIX domain sockets).

This means you can run the resource-heavy **Dingo** node in the background
inside a native Apple container, but run **Adder natively as a standard Go
process directly on your macOS host** (e.g., inside VS Code, with local
debuggers and breakpoints), connecting directly to the relayed socket file
`~/dingo-ipc/node.socket` on your Mac disk!

```text
 ┌────────────────────────── macOS Host ──────────────────────────┐
 │                                                                │
 │  ┌───────────────── Container VM (Linux) ─────────────────┐    │
 │  │                                                        │    │
 │  │  ┌──────────────┐                                      │    │
 │  │  │  Dingo Node  │                                      │    │
 │  │  └──────┬───────┘                                      │    │
 │  └─────────┼──────────────────────────────────────────────┘    │
 │            │ (Creates Socket at /ipc/node.socket)              │
 │            ▼                                                   │
 │    [ ~/dingo-ipc/node.socket File ]  ◄── Host UNIX Socket      │
 │                                          Exposed by            │
 │            ▲                             Hypervisor Socket     │
 │            │                             Relay Path            │
 │            │ (Connects/Dials UNIX Socket)                      │
 │  ┌─────────┴─────────┐                                         │
 │  │  Adder Pipeline   │  ◄── Running natively as standard Go    │
 │  │  (Standard Go)    │      process on Mac (e.g. in VS Code)   │
 │  └───────────────────┘                                         │
 └────────────────────────────────────────────────────────────────┘
```

### Step-by-Step macOS Native Guide

#### Step A: Install `apple/container`

Compile and install the tool on your macOS system by following the official
[apple/container Building from Source](https://github.com/apple/container#building-from-source)
instructions.

#### Step B: Start Dingo with Native Socket Bridging

We provide an automated helper script `./scripts/container-dingo-start.sh` in the
repository root to start the Apple native container services, create your host
socket directories, and launch the Dingo container automatically:

```bash
./scripts/container-dingo-start.sh
```

#### Step C: Run Adder Natively on your macOS Host

Run Adder directly as a macOS Go process pointing to the bridged UNIX socket
file on your Mac disk:

```bash
# Execute Adder on Mac from the repository root, connecting directly to the
# bridged socket file (this command is also printed by the start script!)
go run ./cmd/adder --input chainsync \
  --input-chainsync-socket-path ~/dingo-ipc/node.socket \
  --input-chainsync-network preview \
  --input-chainsync-intersect-tip=true \
  --output log
```

#### Step D: Clean Up and Teardown

To cleanly stop Dingo, delete local socket files, and shut down Apple's
background container daemon, simply run our stop helper script:

```bash
./scripts/container-dingo-stop.sh
```
