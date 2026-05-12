# pulse.New & Options

**Audience:** Go embedders constructing a `Pulse` instance.

`pulse.New(pulse.Options{...})` is the single entry point. There is no
config file, no init function, no global state. Every option is
declared in code (or comes from `PULSE_DATA_DIR` when the field is
left empty).

> **LLM agents using MCP:** the MCP server constructs its own
> `Pulse` instance from CLI flags. Agents don't see this surface.

## The Options struct

From [`pulse.go`](https://github.com/frankbardon/pulse/blob/main/pulse.go):

```go
type Options struct {
    // DataDir is the base directory for cohort files.
    // Defaults to PULSE_DATA_DIR if empty and FS is not set.
    DataDir string

    // FS is an optional custom filesystem.
    // When set, DataDir is ignored for filesystem construction.
    FS afero.Fs

    // DisableDefaults turns off the smart-defaults pass that infers
    // operator Type from the named field's schema type when the caller
    // omits it. Defaults to false (defaults enabled). Predict still
    // computes and reports DefaultsApplied independently — this flag
    // governs only what the runtime mutates on the live request.
    DisableDefaults bool
}
```

## Field reference

### `DataDir string`

The base directory for `.pulse` files. Relative cohort paths
(`{"filename": "data.pulse"}`) resolve against this directory.

| Source | Result |
|---|---|
| Non-empty `Options.DataDir` | Used directly |
| Empty + `FS` non-nil        | `DataDir` is ignored — the FS is the trust boundary |
| Empty + `FS` nil            | Pulse falls back to `fs.Default()`, which reads `PULSE_DATA_DIR` |

Example:

```go
p, err := pulse.New(pulse.Options{DataDir: "/var/data/pulse"})
```

### `FS afero.Fs`

A custom `afero.Fs` implementation. When set, it fully overrides the
filesystem layer — `DataDir` is unused, and `PULSE_DATA_DIR` is not
consulted. Use this for tests (`afero.NewMemMapFs()`) or non-local
backends (S3-backed `afero.Fs`, encrypted overlays, ...).

Example:

```go
import "github.com/spf13/afero"

p, err := pulse.New(pulse.Options{
    FS: afero.NewMemMapFs(),
})
```

See [Custom Filesystems](custom-fs.md) for in-depth usage and the
hermetic-test pattern.

### `DisableDefaults bool`

The runtime smart-defaults pass infers an operator's `Type` from the
named field's schema type when the caller omits it (e.g. `AGG_SUM` on
a numeric field defaults appropriately; categorical fields default
toward `AGG_COUNT`). Set `DisableDefaults = true` to require an
explicit `Type` on every aggregation and grouper — useful when you
want the request to be source-of-truth and never be silently
re-typed.

This option only governs the runtime mutation. `predict` independently
computes and reports `DefaultsApplied` in its result envelope, so
callers can see what would have been inferred even when defaults are
disabled.

CLI parity: `pulse api process --no-defaults`, `pulse api compose
--no-defaults`, `pulse api ask --no-defaults`.

## Defaults at a glance

| Field omitted from `Options` | Effective behaviour |
|---|---|
| `DataDir` and `FS` both empty | Pulse calls `fs.Default()` → reads `PULSE_DATA_DIR` env var. Errors if unset and the operation needs filesystem access. |
| `DataDir` only                | Uses an `afero.NewOsFs()` rooted at `DataDir`. |
| `FS` only                     | Uses the provided FS verbatim. |
| Both                          | `FS` wins; `DataDir` is ignored. |
| `DisableDefaults` omitted     | Defaults enabled. |

## Re-using a Pulse instance

`Pulse` is safe for concurrent use across goroutines once constructed.
The internal registries are read-only after `New`; each `Process`
call constructs fresh stateful operators per request, so multiple
goroutines can call `Process`/`ProcessStream`/`Compose` in parallel
against the same `Pulse`.

For batch parallelism, prefer
[`ComposeParallel`](parallel-compose.md) — it shares the read-only
registries and bounds concurrency for you.

## Tearing down

There is no explicit `Close()` method on `Pulse`. The filesystem is a
borrowed handle; if you supply a custom `FS`, the embedder is
responsible for any cleanup that FS requires. Streaming consumers
should still call `RowIter.Close()` so that the underlying readers
release their buffers.
