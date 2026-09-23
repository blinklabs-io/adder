# Plugin configuration migration

The plugin API and configuration precedence have changed. Update Go consumers
and configurations as described below; no compatibility adapter is provided.

## Go API changes

- `PluginOption.Dest`, `ProcessConfig`, `ProcessEnvVars`, and the built-in
  `NewFromCmdlineOptions` functions are removed.
- `PluginEntry.NewFromOptionsFunc` now has signature
  `func(plugin.Options) (plugin.ManagedPlugin, error)`. Definitions contain scalar
  defaults; instances receive immutable resolved values through typed accessors.
- `plugin.GetPlugin(kind, name, values)` returns `(plugin.ManagedPlugin, error)`.
  `values` is a `map[string]any`; nil means defaults. It does not read environment
  or flags. Unknown plugins and construction failures return descriptive errors.
- `PluginEntry.New(values)` constructs from a definition. For CLI/environment
  composition, call `ResolveConfig(yamlOptions, flags, lookup)` and then
  `Configuration.New(kind, name)`. A nil lookup uses `os.LookupEnv`.
- Plugin YAML maps now use string keys at every level. Registered definitions
  and returned slices are copied. Duplicate registration panics as an authoring
  error; registration does not construct or start a plugin.
- Direct input constructors no longer read the global Kupo URL. Use
  `chainsync.WithKupoUrl` or `mempool.WithKupoUrl`. The CLI still supports the
  top-level `kupo_url` setting through its composition boundary.

```go
p, err := plugin.GetPlugin(plugin.PluginTypeOutput, "log", map[string]any{
    "format": "json",
    "path":   "events.jsonl",
})
if err != nil {
    return err
}
pipe.AddOutput(p)
```

Import the implementation package to register it, as before. Plain constructors
remain available, including their convenience `Start()` methods. A factory
must access only the names and types in its schema; wrong typed accessors panic
as programmer errors, not user configuration errors.

## Lifecycle API changes

`Plugin` is replaced by the `ManagedPlugin` contract. Pipeline add methods,
`PluginEntry.NewFromOptionsFunc`, `PluginEntry.New`, `GetPlugin`, and
`Configuration.New` now use `ManagedPlugin`.
Implement `StartContext`, `Stop`, `Failed`, `Failure`, `Role`, `ErrorChan`,
`InputChan`, and `OutputChan`; use `Base` for shared mechanics if appropriate.
Calls through the interface use `StartContext(ctx)`; `Start()` is only a
convenience on concrete implementations. There is no legacy lifecycle adapter.
See [plugin authoring](plugin-authoring.md) for behavior and required tests.

## Operator changes

Both core and plugin settings now use **explicit CLI > environment > YAML >
defaults**. Compared with `origin/main` at `5dff58c`, core YAML previously beat
environment, while plugin YAML/environment could overwrite explicit flags. Existing deployments with conflicting sources
must review their effective settings.

For example, YAML `plugins.output.log.format: text`, environment
`OUTPUT_LOG_FORMAT=text`, and `--output-log-format=json` now select JSON.
`CARDANO_NETWORK` still overrides `INPUT_CHAINSYNC_NETWORK`, but an explicit
`--input-chainsync-network` overrides both. Other flag and alias spellings are
retained, including the push plugin's camelCase option names.

Unknown YAML fields, duplicate keys, plugin names/types, and plugin option keys
are errors. Every supplied source must parse even if a later source overrides it;
integer plugin options use 32-bit bounds consistently.

`--logging-level`, `LOGGING_LEVEL`, top-level YAML `logging.level`, and Go
`Config.Logging` / `LoggingConfig` are removed. Move their values to
`--output-log-level`, `OUTPUT_LOG_LEVEL`, or `plugins.output.log.level`.
This single threshold defaults to `info` and also controls application diagnostics
on stderr, including when another output is selected. All events are INFO-level;
`warn`/`error` consume without writing them. API delivery is unaffected. See
[log output](../README.md#log-output-plugin) for examples.

The old flag and YAML key are rejected; the old environment variable is ignored.
The CLI validates the resolved log level even when the log output is not selected.

Selected factories reject invalid network names, endpoint formats, intersect
points, polling intervals, and output formats before starting listeners/workers.
Block hashes must be 32 bytes of hex. Unknown UTxO RPC modes no longer enter a
worker retry loop, and invalid intersect points no longer silently disappear in
its registered factory. These checks do not imply remote credentials or
connectivity have been verified; that happens during startup. Unselected plugins
undergo schema/scalar checks but do not need required credentials. Local push and
notification configuration files can be read during construction.

Explicit false, zero, and empty strings survive precedence. In particular,
`--input-mempool-kupo-url=` disables its endpoint even with `KUPO_URL` set.
The top-level Kupo URL supplies a fallback only when plugin YAML omits the key.
Explicit network magic now survives chainsync network-name lookup, including
`4294967295`. Zero retains its existing meaning of selecting the named network's
magic. Telegram construction is offline; authorization remains in `StartContext`.

Core loads start from fresh defaults and commit only on success. Removed settings
return to defaults on reload. Explicit zero genesis settings are preserved.
Configuration reload does not update an already running pipeline and is not
concurrent with readers of the application singleton.

## Notification validation

The `notify-json` compatibility check reads its JSON path, chainsync network,
and custom node address from the resolved snapshot through `Configuration.Options`.
It applies the same CLI > environment > YAML > defaults precedence as plugin
construction and rejects mismatches in the effective values. See
[notifications.go](../cmd/adder/notifications.go) and its source-precedence tests.
