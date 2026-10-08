# Observability

Pulse gives an embedder three independent, optional windows into a running instance. All three default to off, and the off path allocates exactly what it did before they existed.

| `pulse.Options` field | What it is | Cost when nil |
|---|---|---|
| `Logger *slog.Logger` | structured records (stdlib `log/slog`) | nothing written |
| `Hooks *observe.Hooks` | callbacks around every operation and phase | nothing called |
| `Metrics observe.Metrics` | an instrument factory for counters, histograms, gauges | nothing recorded |

Pulse never reads `slog.Default()`, adds no environment variable, and does not change `format_version`. The vocabulary (`Hooks`, `OperationInfo`, the `OperationKind` / `Phase` / `Arm` / `Scope` enums, the instrument interfaces) lives in the small public package `github.com/frankbardon/pulse/observe`, which imports only the standard library.

## Which setup do I need?

| Host shape | Wire it with |
|---|---|
| Logs only (slog) | `Options.Logger = slog.New(handler)` |
| Custom callbacks (audit, tracing of your own) | `Options.Hooks = &observe.Hooks{...}` |
| OpenTelemetry traces and metrics | `contrib/otelpulse`: `Hooks(tp)` + `Metrics(mp)` |
| Prometheus via `client_golang` | `contrib/prompulse`: `Metrics(reg)` |
| The CLI MCP server | `pulse mcp --log-level info --metrics-addr 127.0.0.1:9090` |

```go
p, err := pulse.New(pulse.Options{
    Logger:  slog.New(slog.NewJSONHandler(os.Stderr, nil)),
    Hooks:   otelpulse.Hooks(tracerProvider),
    Metrics: prompulse.Metrics(prometheus.DefaultRegisterer),
})
```

The core module depends on neither OpenTelemetry nor Prometheus. The two adapters are separate modules (`go get github.com/frankbardon/pulse/contrib/otelpulse`, `.../contrib/prompulse`) released at the same version as Pulse itself.

## What counts as an operation

Every operation-bearing `*Pulse` method has an `OperationKind`: `open`, `process`, `process_stream`, `compose`, `compose_parallel`, `process_chain`, `facet`, `facet_schema`, `lookup`, `count_records`, `inspect`, `predict`, `manifest`, `payload_schema`, `import`, `export`, `convert`, `imports_sweep`, `drop`, `sample`, `filter_to_file`, `synth`, `synth_stream`, `profile`, `dedup`, `widen`, the `index_*` and `shard_*` kinds, `template_render` and `template_reload`. `observe.AllOperationKinds()` is the authoritative list.

Pure in-memory getters (`Skills`, `Limits`, `LabelTables`, ...) are not operations, and neither are handles whose real work reaches an instrumented call (`NewCohortBuilder`) or a few filesystem helpers (`CohortArtifacts`, `Imports`, `ResolveImport`, `Watch*`, `ExportReference`, ...): nothing useful to time, and each extra kind widens metric cardinality.

A Compose slot or ProcessChain stage is a **child** operation (`Scope` child, its parent's `Kind`, `Parent` set, `Index` = slot or stage). A parent's result sums its children's rows and bytes, takes the maximum of workers and shards, and reports an `Arm` only when every child ran the same one.

## Logs

Attribute keys are stable; message text is not. Records: `op`, `cohort`, `request_hash`, `duration_ms`, `rows_scanned` (when positive), `code`. At Debug, an extra plan record adds `arm`, `workers`, `shards`, `projected_fields`.

- **Error**: an operation failed (code only). **Warn**: one record per result warning (code only) and one per resource-limit trip (`limit`, `configured`, `observed`). **Info**: `pulse.New` ready; index build/drop and shard create/add/remove/compact completed; imports sweeps and template reloads.
- **Privacy**: logs never carry an error message, expression, filter value, dictionary entry or record. Warnings that exist only as free text are skipped for the same reason.

## Hooks

```go
Hooks: &observe.Hooks{
    OnOperationStart: func(ctx context.Context, i observe.OperationInfo) context.Context { return ctx },
    OnPhase:          func(ctx context.Context, i observe.OperationInfo, p observe.PhaseTiming) {},
    OnOperationEnd:   func(ctx context.Context, i observe.OperationInfo, r observe.OperationResult) {},
}
```

- Hooks run synchronously on the operation's goroutine; keep them fast. A panic is recovered, the operation is unaffected, and it is counted in `pulse_hook_panics_total`.
- The context `OnOperationStart` returns is the one the work, `OnPhase` and `OnOperationEnd` see.
- `OnPhase` fires **when the operation ends**, once per phase that ran, in execution order, just before `OnOperationEnd` (`plan`, `open`, `decode`, `scan`, `reduce`, `post`, `overlay`, `shape`). It is a summary, not a live feed. Streaming operations have no `shape` phase.
- A streaming operation ends when its stream is drained, closed, errors or its context is cancelled. **A stream you abandon without closing never ends**, and its in-flight gauge stays up. Always `Close` an iterator.
- `OperationResult.Code` is `ok`, an error code, or `uncoded`.

## Metrics

| Metric | Type | Labels |
|---|---|---|
| `pulse_operations_total` | counter | `op`, `code`, `scope` |
| `pulse_operation_duration_seconds` | histogram | `op`, `scope` |
| `pulse_phase_duration_seconds` | histogram | `op`, `phase` |
| `pulse_rows_scanned_total` | counter | `op` |
| `pulse_bytes_read_total` | counter | `op` |
| `pulse_limit_trips_total` | counter | `limit` |
| `pulse_hook_panics_total` | counter | `hook` |
| `pulse_operations_in_flight` | up-down counter | `op` |

Operations count and duration cover top-level and child operations (split by `scope`). The rest count top-level operations only, because a parent already carries its children's totals. Labels come from closed sets, so cardinality is bounded.

**Writing your own `observe.Metrics`:** Pulse calls your factory at most once per name and label combination and caches the result. Most instruments are resolved while `New` runs, but `pulse_operations_total` for an error code is resolved on first use, so the factory can be called after `New`, from any goroutine. It must be safe for concurrent use. Histogram buckets are yours to choose.

## OpenTelemetry (`otelpulse`)

`Hooks(tp)` starts one span per operation named `pulse.<kind>`. A child span is named `pulse.process` with the parent's kind in `pulse.op`, `pulse.scope=child` and `pulse.index`. Result attributes (`pulse.code`, `pulse.arm`, `pulse.rows_*`, `pulse.bytes_read`, ...) are set at the end, zero counters omitted; a failure sets the span status and `pulse.error_code`. Phases become span events, timestamped from the span start by the preceding phases' durations, so their placement is approximate. `Metrics(mp)` maps the table above onto a `MeterProvider`.

## Prometheus (`prompulse`)

`Metrics(reg, ...)` registers on any `prometheus.Registerer`; `WithBuckets` overrides the default 0.5 ms to 60 s histogram buckets. A series appears on its first write.

## CLI and `pulse mcp`

`--log-level off|debug|info|warn|error` and `--log-format text|json` are accepted by every leaf and write to stderr only (stdout stays clean for `--json` and for MCP's JSON-RPC). Leaves that never build a Pulse instance (`import`, `convert`, `skills`, `errors`, `version`, `features`) accept the flags but emit nothing. See [Global flags](../cli/flags.md#global-flags).

`pulse mcp --metrics-addr HOST:PORT` serves `GET /metrics` in Prometheus text format from the standard library alone. **The endpoint has no authentication**: bind a loopback address or put it behind your own access control. No port opens unless the flag is given. See [pulse mcp](../cli/mcp.md).

## Contributors

The full contract (attribute keys, phase lists per arm, gates, adapter release procedure) is in `.claude/reference/observability.md`; `make contrib` builds and tests the adapter modules.
