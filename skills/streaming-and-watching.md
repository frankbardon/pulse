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

### Per-chunk + terminal `Components` (v0.20.0)

Projection respects per-operator `ComponentsMergeability` from `descriptor.Manifest.ComponentsSchemas`:

- **Mergeable** (`AGG_SUM/COUNT/AVERAGE`, Welford-family, `AGG_MIN/MAX/RANGE`, `AGG_RATIO`, `AGG_CI_LOWER/UPPER`, set-family, every grouper except `GROUP_QUANTILE`) — `Operator` map carries running state on every chunk. Mid-stream render safe; terminal authoritative.
- **Partial** (`AGG_FREQUENCY`, `AGG_MODE`, `AGG_DISTINCT_COUNT`, `AGG_SET_FREQUENCY`) — folds like mergeable, non-trivial allocation; consumer merges via the same union semantics the orchestrator uses.
- **Non-mergeable** (`AGG_MEDIAN`, `AGG_PERCENTILE`, `GROUP_QUANTILE`) — non-terminal chunks omit per-operator keys (`Operator` nil); floor preserved (`n`, `n_null` aggregator-side; `field`, `label`, `total_n`, `n_null` grouper-side). MUST NOT merge non-terminal chunks.

Identity: terminal chunk's `Components` is `DeepEqual` to the buffered `Process` call's `Response.Components`. See `response-components`.

```go
res, _ := p.ProcessStreamResult(ctx, req)
var terminal *types.ResponseComponents
for chunk := range res.Chunks {
    terminal = chunk.Components
    if chunk.Components != nil { renderRunning(chunk.Components) }
}
<-res.Done; finalise(terminal)
```

Backpressure: 4-deep buffer. `ctx` cancel closes both channels with `StreamCancelled + ctx.Err()`. Mid-stream errors deliver `StreamErrored`.

## `Watch` / `WatchDir`

```go
type ChangeEvent struct {
    Path string; Kind ChangeKind  // Created|Modified|Removed|Renamed
    Hash string; Timestamp time.Time   // Hash empty for Removed
}
ch := p.WatchDir(ctx, "cohorts/", true)
for ev := range ch { handle(ev.Kind, ev.Path, ev.Hash) }
```

No pre-seed — pre-existing files surface as `ChangeCreated` on first tick; drain if you only care about subsequent mutations.

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

Predicates: `Expression` (`FILTER_EXPRESSION` pass-through) or structured `Filterers` (translated, AND-combined). Exactly one set — both empty/both set rejects so the predicate hash is unambiguous.

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

A buffered overlay on a streamable Process downgrades the whole request to buffered — price as buffered regardless of the entry-point annotation.

## Compose streaming vs. overlays

`pulse api compose --stream` emits per-row NDJSON `{index, row}` and bypasses the envelope. The Compose-host overlay fold (`service/compose_overlay.go`) runs only at terminal flush — `ComposedResponse.Overlays[i]` + per-layer `Warnings` appear under `--json` (`data.overlays`) but are absent under `--stream`. Consumers needing Compose overlays MUST run buffered; `Pulse.Compose` / `Pulse.ComposeParallel` callers see overlays on the returned `*ComposedResponse`, never on row events.

## See

- `response-components` — `ResponseComponents` shape + per-operator mergeability + universal floor.
- `overlay-system` — `OverlayStreamability` table + mixed-mode downgrade.
- `compose-requests` — per-slot Components.
- `process-chain` — per-stage Components.
