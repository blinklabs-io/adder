# Architecture diagrams

The [README](../../README.md) and [architecture guide](../architecture.md)
embed these SVGs beside their explanations. The SVG files are editable
vector sources; no HTML viewer, embedded fonts, or browser runtime is required.

- [Input data flow](data-flow.svg): all three input plugins, ordered filters, outputs, and API observation.
- [Pipeline lifecycle](lifecycle.svg): outputs start first; forwarding stops before plugins.
- [ChainSync](chainsync.svg): connection setup, block retrieval, event delivery, and shutdown.
- [Tray](tray.svg): configuration, service management, and local notification rules.
- Dingo connections: [Docker Compose](dingo-compose.svg) and [native macOS](dingo-macos.svg).

Before changing a sequence, check [pipeline startup](../../pipeline/pipeline.go),
[stop order](../../pipeline/topology.go), and [ChainSync](../../input/chainsync/chainsync.go).
Keep the guide's captions in sync, including failure paths and delivery limits.

The sequence layouts were originally exported from Archify and simplified to
static SVG. Its [MIT license](ARCHIFY-LICENSE) is retained.
