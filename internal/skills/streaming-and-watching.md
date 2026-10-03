---
name: streaming-and-watching
description: Reactive Pulse primitives — Request.Hash() cache keys, StreamResult[T] incremental output with per-chunk + terminal Components, Watch / WatchDir mutation observation, FilterToFileWithRequest deterministic derived cohorts, manifest CommandAnnotations caching policy.
type: guide
kind: design
applies_to: process, predict, manifest
covers: [Request.Hash, StreamResult, Watch, WatchDir, FilterToFileWithRequest, CommandAnnotations]
---

# Streaming & watching

Four primitives for deterministic identity, incremental output, and reactive observation. Manifest annotations wire caching policy.

## `Request.Hash()`

`Hash() string` returns a 32-char hex digest of canonical-JSON form. Supported: `Request`, `ComposedRequest`, `FacetRequest`, `ChainRequest`, `synth.Spec`. Lower-level: `types.CanonicalHash(tag, v)`.

Guarantees: same logical request → same hash across processes/versions; round-trip stable; field-order invariant; default-normalising (`Limit: 0` ≡ omitted); `-0.0` ≡ `0.0`; namespace tag separates shapes (e.g. `Request` vs `ComposedRequest`).

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
    Done   <-chan StreamTerminator   // CompletedAt, TotalRows, Status, Error
}
type StreamChunk[T any] struct {
    Sequence int; Data T; Progress float64
    Components *types.ResponseComponents
}
```

Variants: `ProcessStreamResult` wraps `ProcessStream`; `SynthStream` wraps `synth.SynthBytes`.

### Per-chunk + terminal `Components`

What a chunk's `Components` holds depends on each operator's `mergeability` class — read it from `manifest.components_schemas`, never from the operator's name; predict flags every `none` slot `buffered_components: true` before you run.

- **`mergeable`** — `operator` carries running state on every chunk. Rendering mid-stream is safe; the terminal chunk is authoritative.
- **`partial`** — folds like mergeable but the state grows (maps / sets); a consumer may union chunk partials the way the engine does, or just wait for the terminal chunk.
- **`none`** — the operator needs the whole sorted input (medians, percentiles, quantile bands): non-terminal chunks carry the floor only (`n`, `n_null`; grouper `field`, `label`, `total_n`, `n_null`) with `operator` nil. MUST NOT merge non-terminal chunks.

Identity: the terminal chunk's `Components` is `DeepEqual` to the buffered `Process` result's. With components disabled every chunk carries `nil`.

```go
res, _ := p.ProcessStreamResult(ctx, req)
var terminal *types.ResponseComponents
for chunk := range res.Chunks {
    terminal = chunk.Components
    if chunk.Components != nil { renderRunning(chunk.Components) }
}
<-res.Done; finalise(terminal)
```

`ctx` cancel closes both channels with `StreamCancelled + ctx.Err()`. Mid-stream errors deliver `StreamErrored`.

### Two-pass attributes

Attributes that need a whole-cohort statistic first (a z-score, a regression fit, a declared two-pass extension) stream in two passes: they keep declared order and read earlier labels as buffered does; each dependent layer adds a scan.

## `Watch` / `WatchDir`

```go
type ChangeEvent struct {
    Path string; Kind ChangeKind  // Created|Modified|Removed|Renamed
    Hash string; Timestamp time.Time   // Hash empty for Removed
}
ch := p.WatchDir(ctx, "cohorts/", true)
for ev := range ch { handle(ev.Kind, ev.Path, ev.Hash) }
```

No pre-seed — pre-existing files surface as `ChangeCreated` on the first tick.

`WatchOptions`: `PollInterval` (250 ms), `CoalesceWindow` (100 ms; `0` disables), `HashPrefixBytes` (64 KiB; `< 0` = whole file), `Recursive`, `Suffix` (`.pulse` for `WatchDir`). Network filesystems should raise `PollInterval` to ~30 s.

Atomic-write coalescing: `Removed(temp) + Created(target)` folds into `ChangeRenamed(target)` when paths share a directory, the temp matches `<target>.tmp`/`.partial`/`.swp`, `~<target>`, hidden-dotfile, or `<target>.NNNN`, inside `CoalesceWindow`.

## `FilterToFileWithRequest`

```go
res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
    SourcePath: "cohort-2025-Q3.pulse",
    Expression: "age >= 18 && state in [\"CA\", \"NY\"]",
    OutputDir:  "derived/",
})
```

Guarantees: deterministic name `{source-hash[:16]}_{predicate-hash[:16]}.pulse` when `OutputName` empty; atomic write via `OutputDir/.<name>.partial` + rename; dedup by pre-existence (re-reads hash + row count, returns `Reused: true`).

Predicates: `Expression` (an expression-filter string, `expression-language`) or structured `Filterers` (translated, AND-combined). Exactly one set — both empty/both set rejects so the predicate hash is unambiguous.

## Manifest `CommandAnnotations`

Every `Manifest.Commands` (CLI leaves) and `Manifest.Operations` (library-only: `filter_to_file`, `process_stream`, `synth_stream`, `watch`) carries:

```yaml
- name: process_stream
  annotations: {streamable: true, deterministic: true, expensive: true}
- name: synth_stream
  annotations: {streamable: true, deterministic: false, expensive: true}
- name: watch
  annotations: {streamable: true, deterministic: false, expensive: false}
```

`streamable` — has a `*Stream` variant. `deterministic` — same inputs ⇒ byte-identical output; cache keyed by `req.Hash()` + source hash. Non-deterministic entries MUST NOT be cached as stable. `expensive` — hint.

A buffered overlay downgrades a streamable Process to buffered — price it as buffered.

## Compose streaming vs. overlays

`pulse api compose --stream` emits per-row NDJSON `{index, row}` and bypasses the envelope. The Compose-host overlay fold runs only at terminal flush — `ComposedResponse.Overlays[i]` + per-layer `Warnings` appear under `--json` (`data.overlays`) but are absent under `--stream`. Consumers needing Compose overlays MUST run buffered; `Pulse.Compose` / `Pulse.ComposeParallel` callers see overlays on the returned `*ComposedResponse`, never on row events.

## See

`response-components` (shape, mergeability classes) · `overlay-system` (overlay streamability)<!-- feature: capability:compose --> · `compose-requests` (per-slot Components)<!-- /feature --><!-- feature: capability:process_chain --> · `process-chain` (per-stage Components)<!-- /feature -->.
