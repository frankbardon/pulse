```yaml
name: streaming-and-watching
description: Reactive Pulse primitives — Request.Hash() cache keys, StreamResult[T] incremental output with per-chunk + terminal Components, Watch / WatchDir mutation observation, FilterToFileWithRequest deterministic derived cohorts, manifest CommandAnnotations caching policy.
type: guide
kind: design
applies_to: process, predict, manifest
covers: [Request.Hash, StreamResult, Watch, WatchDir, FilterToFileWithRequest, CommandAnnotations]
```

# Streaming & watching

## `Request.Hash()`

`Hash()` is a 32-char hex digest of canonical JSON. Supported: `Request`, `ComposedRequest`, `FacetRequest`, `ChainRequest`, `synth.Spec`. Lower-level: `types.CanonicalHash(tag, v)`.

Guarantees: stable across processes/versions; field-order invariant; `null` / zero ≡ omitted, but `"disable_components": false` is not; `-0.0` ≡ `0.0`; a namespace tag separates shapes. `return` is part of the hash.

```go
key := req.Hash()
if v, ok := cache.Get(key); ok { return v }
resp, _ := p.Process(ctx, req); cache.Set(key, resp)
```

## `StreamResult[T]`

```go
type StreamResult[T any] struct {
    Header StreamHeader              // RequestHash, EstimatedTotal (-1 unknown), StartedAt
    Chunks <-chan StreamChunk[T]     // 4-deep; producer never drops
    Done   <-chan StreamTerminator   // CompletedAt, TotalRows, Status, Error, Warnings
}
type StreamChunk[T any] struct {
    Sequence int; Data T; Progress float64
    Components *types.ResponseComponents
}
```

Variants: `ProcessStreamResult` wraps `ProcessStream`; `SynthStream` wraps `synth.SynthBytes`. Run-time warnings: iterator `Warnings()` when drained; CLI `--stream` ends with one `{"warnings":[...]}` line.

**Grouped output arrives at terminal flush:** a group exists only once every record is read, so a grouped request's rows arrive after the scan on every arm (serial, `ShardWorkers`, `DecodeWorkers`).

### Per-chunk + terminal `Components`

What a chunk's `Components` holds depends on each operator's `mergeability` class — read it from `manifest.components_schemas`, never from the operator's name; predict flags every `none` slot `buffered_components: true` before you run.

- **`mergeable`** — `operator` carries running state on every chunk. Rendering mid-stream is safe; the terminal chunk is authoritative.
- **`partial`** — folds like mergeable but the state grows (maps / sets); union chunk partials as the engine does, or wait for the terminal chunk.
- **`none`** — the operator needs the whole sorted input (medians, percentiles, quantile bands): non-terminal chunks carry the floor only (`n`, `n_null`; grouper `field`, `label`, `total_n`, `n_null`) with `operator` nil. MUST NOT merge non-terminal chunks.

Identity: the terminal chunk's `Components` is `DeepEqual` to the buffered `Process` result's. With components disabled every chunk carries `nil`.

A `return` block shapes streamed rows (selected columns, wire precision via `returnshape.MarshalRow`); `returned` rides the terminal chunk only. See [`response-shaping`](response-shaping.md).

```go
res, _ := p.ProcessStreamResult(ctx, req)
var terminal *types.ResponseComponents
for chunk := range res.Chunks {
    terminal = chunk.Components
    if chunk.Components != nil { renderRunning(chunk.Components) }
}
<-res.Done; finalise(terminal)
```

`ctx` cancel closes both channels (`StreamCancelled` + `ctx.Err()`); a mid-stream error is `StreamErrored`.

### Two-pass attributes

Attributes needing a whole-cohort statistic (z-score, regression fit, two-pass extension) stream in two passes in declared order; each dependent layer adds a scan.

## `Watch` / `WatchDir`

`ChangeEvent{Path, Kind, Hash, Timestamp}` — `Kind` is `Created|Modified|Removed|Renamed`; `Hash` is empty for `Removed`.

```go
for ev := range p.WatchDir(ctx, "cohorts/", true) { handle(ev.Kind, ev.Path, ev.Hash) }
```

No pre-seed: existing files surface as `ChangeCreated` on the first tick.

`WatchOptions`: `PollInterval` (250 ms), `CoalesceWindow` (100 ms; `0` disables), `HashPrefixBytes` (64 KiB; `< 0` = whole file), `Recursive`, `Suffix` (`.pulse` for `WatchDir`). Network FS: ~30s.

Atomic-write coalescing: `Removed(temp) + Created(target)` folds into `ChangeRenamed(target)` when both share a directory, the temp has an editor/atomic-write name, and both fall inside `CoalesceWindow`.

## `FilterToFileWithRequest`

```go
res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
    SourcePath: "cohort-2025-Q3.pulse",
    Expression: "age >= 18",
    OutputDir:  "derived/",
})
```

Guarantees: deterministic name `{source-hash[:16]}_{predicate-hash[:16]}.pulse` when `OutputName` empty; atomic write (`.<name>.partial` + rename); dedup by pre-existence (`Reused: true`).

Predicates: `Expression` ([`expression-language`](expression-language.md)) or structured `Filterers` (AND, as in `Process`) — exactly one. `TimeZone` is the zone `to_day()` and a zone-capable filterer read (over the instance default); applied zones join the hash, as does each referenced range / lookup table's content. Embedder code (extension filterer, expr fn, `Lookup` table) never reuses.

`Models` (`[{name, path}]`) loads saved models for `coef()`; their content hashes join the predicate hash.

## Manifest `CommandAnnotations`

Every `Manifest.Commands` (CLI leaves) and `Manifest.Operations` (library-only: `filter_to_file`, `process_stream`, `synth_stream`, `watch`) carries `annotations: {streamable, deterministic, expensive}` — `process_stream` all true; `synth_stream` not deterministic; `watch` neither deterministic nor expensive.

`streamable` — has a `*Stream` variant. `deterministic` — same inputs ⇒ byte-identical output; cache by `req.Hash()` + source hash. Non-deterministic entries MUST NOT be cached as stable. `expensive`: a hint.

A buffered overlay makes a streamable Process buffered — price it so.

## Compose streaming vs. overlays

`pulse api compose --stream` emits per-row NDJSON `{index, row}` and bypasses the envelope. The Compose-host overlay fold runs only at terminal flush — `ComposedResponse.Overlays[i]` + per-layer `Warnings` appear under `--json` (`data.overlays`) but are absent under `--stream`. Consumers needing Compose overlays MUST run buffered (`Pulse.Compose` / `ComposeParallel`).

## See

[`response-components`](response-components.md) (shape, mergeability classes) · [`response-shaping`](response-shaping.md) (`return`) · [`overlay-system`](overlay-system.md) (overlay streamability) · [`compose-requests`](compose-requests.md) (per-slot Components) · [`process-chain`](process-chain.md) (per-stage Components).
