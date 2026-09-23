# Architecture diagrams

The [README](../../README.md) and [architecture guide](../architecture.md)
embed these SVGs beside their explanations. The sequence diagrams also keep
editable Excalidraw sources; no HTML viewer or browser runtime is required.

- [Input data flow](data-flow.svg): all three input plugins, ordered filters, outputs, and API observation.
- Pipeline lifecycle: [SVG](lifecycle.svg) and [Excalidraw source](lifecycle.excalidraw).
- ChainSync: [SVG](chainsync.svg) and [Excalidraw source](chainsync.excalidraw).
- [Tray](tray.svg): configuration, service management, and local notification rules.
- Dingo connections: [Docker Compose](dingo-compose.svg) and [native macOS](dingo-macos.svg).

Before changing a sequence, check [pipeline startup](../../pipeline/pipeline.go),
[stop order](../../pipeline/topology.go), and [ChainSync](../../input/chainsync/chainsync.go).
Keep the guide's captions in sync, including failure paths and delivery limits.
