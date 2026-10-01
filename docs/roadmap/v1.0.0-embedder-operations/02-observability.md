# 02 — Observability: logger, timing callbacks, opt-in metrics

## The constraint

Pulse is not always a server. It runs inside a CLI invocation, a batch job, a web service, a desktop app or an MCP server. So observability can't assume an HTTP endpoint, a metrics registry or a log destination. The idiomatic Go answer is **inversion of control**: Pulse *emits*, and the host decides where it goes. The core module takes **no** dependency on any observability vendor.

## Three layers, each independent and optional

### 1. Logger: `Options.Logger *slog.Logger`

- Standard-library `log/slog`, so any backend works (zap, zerolog, logrus and others all have slog handlers).
- **`nil` means silent**: the default discards, so embedding Pulse never produces unexpected output.
- **What Pulse logs** (levels fixed and documented):
  - `Debug`: plan decisions (projected fields, fused vs buffered, worker counts, shard fan-out).
  - `Info`: lifecycle (instance created, templates reloaded, cohort scan done).
  - `Warn`: everything that becomes a response warning, plus recoverable conditions.
  - `Error`: failed operations.
- **Attributes are structured and stable-ish:** `op`, `cohort`, `request_hash`, `duration_ms`, `rows_scanned`, `code`. Log text is explicitly *not* covered by `STABILITY.md`.
- **Request-scoped attributes:** Pulse uses `slog`'s context-aware calls (`InfoContext`) everywhere, so a host whose handler pulls a request or trace ID out of `ctx` gets correlation for free.
- **Never logs row data or field values.** Only names, counts and timings. This is a privacy guarantee worth stating in the docs.

### 2. Timing callbacks: `Options.Hooks`

```go
type Hooks struct {
    OnOperationStart func(ctx context.Context, op OperationInfo) context.Context // may return a derived ctx (e.g. a span)
    OnOperationEnd   func(ctx context.Context, op OperationInfo, res OperationResult)
    OnPhase          func(ctx context.Context, op OperationInfo, phase PhaseTiming) // optional, finer-grained
}
```

- **`OperationInfo`:** kind (process, compose, facet, chain, stream, lookup, import, export, synth, …), cohort, request hash, slot/stage index.
- **`OperationResult`:**
  - duration, error code (if any);
  - `rows_scanned`, `rows_matched`, `rows_out`, `bytes_read`;
  - `shards`, `workers`;
  - whether the run was fused, projected or streamed.

  Most of these counters already exist in `Response.Components.Run`, and the hook simply receives them.
- **`PhaseTiming`:** `{phase: open | predict | decode | filter | aggregate | overlay | serialize, duration}`, so a host can see *where* time went.
- **Why `OnOperationStart` returns a context:** that single signature is what lets a host start a tracing span, store it in the context and end it in `OnOperationEnd`. Tracing works without Pulse importing OpenTelemetry.
- **Hooks are synchronous and must be fast.** They are documented as such, and a panic in a hook is recovered and logged, never allowed to crash the operation.
- **All `nil` by default.** There is zero overhead when unset (one nil check per operation and per phase).

### 3. Metrics: opt-in, via a tiny interface

```go
type Metrics interface {
    Counter(name string, labels ...Label) Counter     // Add(float64)
    Histogram(name string, labels ...Label) Histogram // Observe(float64)
}
```

- **`Options.Metrics` is `nil` by default, so no metrics exist** (opt-in, as decided).
- Pulse emits a small, documented set: `pulse_operations_total{op,code}`, `pulse_operation_duration_seconds{op}`, `pulse_rows_scanned_total{op}`, `pulse_bytes_read_total`, `pulse_limit_trips_total{limit}`, `pulse_cache_hits_total` (where applicable).
- **Label cardinality is bounded by design:** no cohort names, no request hashes, only enumerated values. A metrics backend can't be blown up by user input.
- **Adapters live outside the core module** so the core stays dependency-free:
  - `github.com/frankbardon/pulse/contrib/otelpulse`: an OpenTelemetry metrics + tracing adapter (implements `Metrics`, plus `Hooks` that create spans);
  - `github.com/frankbardon/pulse/contrib/prompulse`: a Prometheus adapter.

  They are separate Go modules in the same repository, so importing Pulse never pulls in OTel or Prometheus.

## How each host shape uses it

| Host | Typical setup |
|---|---|
| Downstream Go library / batch job | `Logger` only, or nothing at all |
| Web service with OTel | `otelpulse.Hooks()` + `otelpulse.Metrics(meterProvider)`; spans nest under the service's request span via `ctx` |
| Service with Prometheus | `prompulse.Metrics(registry)`; the service exposes `/metrics` itself (Pulse never opens a port) |
| `pulse mcp` (Pulse *is* the process) | CLI flags: `--log-level`, `--log-format text\|json` (stderr only; stdout is the MCP protocol); opt-in `--metrics-addr :9090` serving Prometheus from the binary. This is the **only** place Pulse opens a port, and only when asked |
| CLI one-shots | `--log-level debug` for troubleshooting; default silent |

## Gates

- **`TestObservabilityDefaultsSilent`:** with default `Options`, an operation writes nothing to stdout or stderr and calls no hook.
- **`TestObservabilityNoRowData`:** under a capturing handler at `Debug`, no logged attribute contains a record value from the fixture cohorts.
- **`TestCoreModuleNoObservabilityDeps`:** `go.mod` of the core module imports no OTel or Prometheus package.
- **`TestHookPanicRecovered`.**

## Deliverables

- [ ] `Options.Logger` (`slog`, nil = silent); context-aware logging at documented levels; no row data
- [ ] `Options.Hooks`: operation start/end (context-returning start), phase timings; panic-safe
- [ ] `Options.Metrics` interface (opt-in) with the documented metric set and bounded labels
- [ ] `contrib/otelpulse` and `contrib/prompulse` as separate modules
- [ ] `pulse mcp` / CLI flags `--log-level`, `--log-format`, `--metrics-addr` (opt-in)
- [ ] Silence, no-row-data, dependency and panic gates; embedder docs page "Observability"
