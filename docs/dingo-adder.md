# Dingo-Adder Connection Architecture

This document describes how **Dingo** and **Adder** are connected and
interact with each other inside our local development environment on the
`preview` network.

## Connection Graph (Text Diagram)

```text
┌────────────────────────────────────────────────────────────────────────┐
│                                 Host                                   │
│                                                                        │
│  ┌───────────────────────── Docker Desktop VM ──────────────────────┐  │
│  │                                                                  │  │
│  │  ┌────────────────────────┐          ┌────────────────────────┐  │  │
│  │  │    dingo Container     │          │    adder Container     │  │  │
│  │  │                        │          │                        │  │  │
│  │  │  ┌──────────────────┐  │          │  ┌──────────────────┐  │  │  │
│  │  │  │    Dingo Node    │  │          │  │  Adder Pipeline  │  │  │  │
│  │  │  └────────┬─────────┘  │          │  └────────▲─────────┘  │  │  │
│  │  └───────────┼────────────┘          └───────────┼────────────┘  │  │
│  │              │                                   │               │  │
│  │              │ (Creates Socket)                  │ (Dials Unix   │  │
│  │              │                                   │  Socket)      │  │
│  │   ┌──────────▼───────────────────────────────────┴──────────┐    │  │
│  │   │             Shared Docker Volume: dingo-ipc             │    │  │
│  │   │                                                         │    │  │
│  │   │               Mount Point: /ipc (Socket)                │    │  │
│  │   │             File: /ipc/node.socket                      │    │  │
│  │   └─────────────────────────────────────────────────────────┘    │  │
│  │                                                                  │  │
│  └──────────────────────────────────▲───────────────────────────────┘  │
│                                     │                                  │
│                                     │ (TCP Port 3001)                  │
│                                     │                                  │
│                      ┌──────────────┴──────────────┐                   │
│                      │   Cardano Preview Network   │                   │
│                      └─────────────────────────────┘                   │
└────────────────────────────────────────────────────────────────────────┘
```

## Detailed Data Flow

1. **Cardano Syncing:** Dingo connects outward to public peer nodes on the
    Cardano `preview` network over TCP port 3001 using the **Node-to-Node
    (NtN)** Ouroboros protocol, syncing blocks into its local badger database.
2. **UNIX Socket Creation:** Dingo initializes and creates a UNIX Domain Socket
    at `/ipc/node.socket`. Since the folder `/ipc` is mapped to the `dingo-ipc`
    named volume, the socket file is stored directly within the Docker internal
    VM storage.
3. **N2C Pipeline Connection:** Adder mounts the same `dingo-ipc` volume at
    `/ipc`. Adder starts up, reads `config-preview.yaml`, and initiates a
    connection to `/ipc/node.socket` using the **Node-to-Client (N2C)**
    Ouroboros protocol.
4. **Log Emission:** The chainsync block and rollback events are streamed
    through the Go channels in Adder's pipeline, and printed directly to stdout,
    which Docker captures and displays on your console.

---

## macOS Native Setup

For step-by-step instructions on running this connection natively on macOS
(with UNIX domain socket bridging), see the
[Dingo macOS Setup Guide](./dingo-macos-setup.md).
