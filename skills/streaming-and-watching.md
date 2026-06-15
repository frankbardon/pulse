---
name: streaming-and-watching
description: Build reactive consumers on top of Pulse — Request.Hash() for cache keys, StreamResult[T] for incremental output, Watch/WatchDir for file-change observation, FilterToFileWithRequest for deterministic derived cohorts, manifest CommandAnnotations for caching policy, and ChainRequest dual-slot overlays for per-stage + whole-chain decoration. Use when wiring Pulse into long-running services, building materialization caches, reacting to .pulse file mutations, or stacking overlays across a ProcessChain pipeline.
type: guide
applies_to: process, compose, predict, manifest, process-chain
---

# Streaming & Watching

<skill_overview>
Pulse exposes four primitives for callers that need deterministic identity, incremental output, or reactive observation:

1. **`Request.Hash()`** — stable canonical identifier for any request shape, suitable for cache keys, dedup, and filenames.
2. **`StreamResult[T]`** — canonical generic shape for incremental output (`ProcessStreamResult`, `SynthStream`).
3. **`Pulse.Watch` / `Pulse.WatchDir`** — coalesced poll-based observation of `.pulse` file changes.
4. **`Pulse.FilterToFileWithRequest`** — deterministic, dedup-aware filter-to-file with `{source-hash}_{predicate-hash}.pulse` output naming.

Manifest annotations (`CommandAnnotations` on every `Command` and `Operation`) let consumers query `streamable / deterministic / expensive` per entry to decide caching policy without trial and error.
</skill_overview>

<reference>
## Request hashing

Every request type implements `Hash() string` returning a 32-character hex digest of the canonical JSON form:

```go
hash := req.Hash() // 32-char hex, e.g. "a3f1c0d2e4567890abcdef0123456789"
```

Supported types: `pulse.Request`, `pulse.ComposedRequest`, `pulse.FacetRequest`, `pulse.ChainRequest`, `synth.Spec`. The lower-level helper `types.CanonicalHash(tag, v)` hashes any JSON-serializable value with a caller-chosen namespace tag.

**Guarantees:**

- Same logical request → same hash, across processes and Pulse versions where semantics are unchanged.
- Round-trip stable: `json.Marshal` → `json.Unmarshal` → `Hash()` returns the original digest.
- Field-order invariant (struct field order normalised by the canonical encoder, map keys sorted).
- Default-normalising: an explicit zero (`Limit: 0`) and the omitted equivalent hash identically because every request struct field carries `omitempty` JSON tags.
- Negative-zero collapse: `-0.0` hashes identically to `0.0`.
- Namespace tag separates request shapes — a `Request` and a `ComposedRequest` with identical wire bytes never collide.

**Use cases:** cache keys for `ProcessStreamResult` outputs, filename suffix for derived cohorts, dedup keys for `Compose` slot results, idempotency tokens.

```go
// Cache lookup keyed by request hash.
cacheKey := req.Hash()
if cached, ok := cache.Get(cacheKey); ok {
    return cached
}
resp, err := p.Process(ctx, req)
if err == nil {
    cache.Set(cacheKey, resp)
}
```
</reference>

<reference>
## StreamResult[T]

`StreamResult[T]` is the canonical streaming shape returned by any `*Stream` variant. Three phases: a single `Header` describing the stream, zero-or-more `Chunks` carrying the payload, and a single `Terminator` describing how the stream ended.

```go
type StreamResult[T any] struct {
    Header StreamHeader
    Chunks <-chan StreamChunk[T]
    Done   <-chan StreamTerminator
}

type StreamHeader struct {
    RequestHash    string    // From req.Hash()
    EstimatedTotal int64     // Best-effort; -1 if unknown
    StartedAt      time.Time
}

type StreamChunk[T any] struct {
    Sequence   int          // Monotonic 0-based
    Data       T
    Progress   float64      // 0.0–1.0, or -1 if unknown
    Components *types.ResponseComponents `json:"components,omitempty"`
}

type StreamTerminator struct {
    CompletedAt time.Time
    TotalRows   int64
    Status      StreamStatus  // Completed | Cancelled | Errored
    Error       error
}
```

### Available variants

- `Pulse.ProcessStreamResult(ctx, req)` — wraps the existing `ProcessStream` engine; row chunks plus deterministic terminator.
- `Pulse.SynthStream(ctx, spec, opts)` — wraps `synth.SynthBytes`; respondent chunks plus deterministic terminator.

### Receiver pattern

```go
res, err := p.ProcessStreamResult(ctx, req)
if err != nil { return err }

for chunk := range res.Chunks {
    handle(chunk.Sequence, chunk.Data, chunk.Progress)
}
term := <-res.Done
if term.Status != pulse.StreamCompleted {
    return fmt.Errorf("stream %s: %w", term.Status, term.Error)
}
fmt.Printf("delivered %d rows in %s\n",
    term.TotalRows, term.CompletedAt.Sub(res.Header.StartedAt))
```

### Backpressure & cancellation

- `Chunks` carries a 4-deep buffer. Slow consumers slow the producer — the producer never drops chunks.
- Cancelling `ctx` causes the producer to emit a `StreamTerminator{Status: StreamCancelled, Error: ctx.Err()}` and close both channels.
- A mid-stream error closes `Chunks` early and delivers `StreamTerminator{Status: StreamErrored, Error: err}` on `Done`.
- The non-streaming variants (`Process`, `Synth`) remain — they are convenience wrappers that drain the stream and return the full result.

### Per-chunk `Components` — consumer-side merge contract

Each `StreamChunk[Row]` returned by `ProcessStreamResult` carries a `Components *types.ResponseComponents` projection of the run's constituent-parts payload. The projection follows the per-operator `ComponentsMergeability` declared in the manifest (descriptor surface, source of truth):

- **Mergeable** operators (`AGG_SUM`, `AGG_COUNT`, `AGG_AVERAGE`, `AGG_VARIANCE`, Welford-family, `AGG_RANGE`, `AGG_MIN`, `AGG_MAX`, `AGG_NULL_COUNT`, `AGG_WEIGHTED_MEAN`, `AGG_RATIO`, `AGG_CI_LOWER`, `AGG_CI_UPPER`, set-family aggregators) — the per-operator `Operator` map carries the running state on every chunk. Consumers may render mid-stream; the terminal chunk carries the authoritative final.
- **Partial** operators (`AGG_FREQUENCY`, `AGG_MODE`, `AGG_DISTINCT_COUNT`, `AGG_SET_FREQUENCY`) — fold like mergeable but at non-trivial allocation cost. The Operator map rides every chunk and merges client-side via the same union semantics the orchestrator uses.
- **Non-mergeable** operators (`AGG_MEDIAN`, `AGG_PERCENTILE`) — chunks before the terminal omit the per-operator keys (Operator is `nil`) but preserve the universal floor (`N`, `NNull`, `Label`). The terminal chunk carries the buffered-only payload. Consumers MUST NOT attempt to merge non-terminal chunks for these slots.

Identity: the terminal chunk's `Components` is byte-equal (DeepEqual) to the equivalent buffered `Process` call's `Response.Components`. Consumers needing a one-shot result can drain to the terminal and use that chunk's `Components` exclusively.

```go
res, _ := p.ProcessStreamResult(ctx, req)
var terminal *types.ResponseComponents
for chunk := range res.Chunks {
    terminal = chunk.Components            // last assignment wins
    if chunk.Components != nil {
        renderRunning(chunk.Components)    // safe for mergeable / partial slots
    }
}
<-res.Done
finalise(terminal)                          // byte-equal to buffered Process
```

Allocation note: the per-chunk projection clones only the `Aggregations` slice (`O(slots)` per chunk) and shares `Groupers` / `Crosstab` / `Filterers` / `Run` with the buffered original. The mergeability vector is computed once per stream and reused across every chunk.
</reference>

<reference>
## Watch & WatchDir

`Pulse.Watch` observes a single `.pulse` file; `Pulse.WatchDir` walks a directory. Both emit `ChangeEvent` records on a channel that closes when the context cancels.

```go
type ChangeEvent struct {
    Path      string
    Kind      ChangeKind  // Created | Modified | Removed | Renamed
    Hash      string      // SHA-256 hex of the file's leading bytes (empty for Removed)
    Timestamp time.Time
}
```

### Default behaviour

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

ch := p.WatchDir(ctx, "cohorts/", true /* recursive */)
for ev := range ch {
    fmt.Println(ev.Kind, ev.Path, ev.Hash)
}
```

The watcher does **not** pre-seed its state map — files that already exist when the watcher starts surface as `ChangeCreated` on the first tick. If you only care about subsequent mutations, drain the first batch.

### WatchOptions

```go
type WatchOptions struct {
    PollInterval    time.Duration  // Default 250ms
    CoalesceWindow  time.Duration  // Default 100ms (0 disables coalescing)
    HashPrefixBytes int            // Default 64 KiB (< 0 hashes entire file)
    Recursive       bool           // WatchDir-only
    Suffix          string         // Default ".pulse" for WatchDir, "" for Watch
}
```

Network filesystems should raise `PollInterval` to ~30s — stat-poll cost dominates over fsnotify-style edge events anyway.

### Atomic-write coalescing

The watcher folds a `Removed(temp)` followed by `Created(target)` into a single `ChangeRenamed(target)` event when:

- Both paths share a directory.
- The temp basename looks like a canonical scratch sibling (`<target>.tmp`, `<target>.partial`, `<target>.swp`, `~<target>`, hidden-dotfile variants, or `<target>.NNNN`).
- The two events fall inside `CoalesceWindow` of each other.

This matches the rename-from-temp idiom most atomic-write libraries use.

### Hashing

`ChangeEvent.Hash` is computed by re-reading the file's first `HashPrefixBytes` bytes. For `.pulse` files the default 64 KiB easily covers the header + schema + first dictionary block, which is enough to detect any structural change. Pass `HashPrefixBytes: -1` to hash the entire file when collision risk on small mutations matters.
</reference>

<reference>
## Deterministic FilterToFile

`Pulse.FilterToFileWithRequest` runs a filter expression (or structured `Filterers` slice) against a source cohort and writes the survivors to a new `.pulse` file. Three guarantees make it dedup-safe:

1. **Deterministic naming.** When `OutputName` is empty, the output basename is `{source-hash[:16]}_{predicate-hash[:16]}.pulse`. Independent consumers reach the same expected path without coordination.
2. **Atomic write.** The engine stages to `OutputDir/.<name>.partial` and renames into place only after the filter completes. A partially-written `.pulse` file never appears at the target path.
3. **Dedup by pre-existence.** Before running the filter, the engine checks `outputPath`. If a file already exists there, it re-reads the hash and row count and returns the existing result with `Reused: true`.

```go
res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
    SourcePath: "cohort-2025-Q3.pulse",
    Expression: "age >= 18 && state in [\"CA\", \"NY\"]",
    OutputDir:  "derived/",
})
if err != nil { return err }
fmt.Printf("%s [%s] %d rows (reused=%v)\n",
    res.OutputPath, res.OutputHash, res.RowCount, res.Reused)
```

### Two predicate shapes

- **`Expression`** — pass-through to the `FILTER_EXPRESSION` engine. Use for expr-lang predicates.
- **`Filterers`** — structured `[]*types.Filterer` translated into the equivalent expression and AND-combined. Covers `FILTER_INCLUDE`, `FILTER_EXCLUDE`, `FILTER_RANGE`, `FILTER_NULL`, and pass-through `FILTER_EXPRESSION`.

Exactly one of `Expression` or `Filterers` must be set — both empty or both set is a configuration error so the predicate hash is unambiguous.

### Caching pattern

`FilterToFileWithRequest` is the building block for shared materialization caches: many consumers can ask for the same derived cohort without coordination, and only the first request pays the filter cost. The deterministic output path means a downstream caller can compute the expected path from the source + predicate alone, check for existence, and skip the call entirely on a hit.
</reference>

<reference>
## Manifest annotations

Every entry in `Manifest.Commands` and `Manifest.Operations` carries a `CommandAnnotations` block:

```yaml
commands:
  - name: process
    annotations:
      streamable: true
      deterministic: true
      expensive: true
operations:
  - name: filter_to_file
    annotations:
      streamable: false
      deterministic: true
      expensive: true
  - name: synth_stream
    annotations:
      streamable: true
      deterministic: false   # random unless spec carries a seed
      expensive: true
  - name: watch
    annotations:
      streamable: true
      deterministic: false
      expensive: false
```

### Semantics

- **`streamable`** — the entry has a `*Stream` variant; callers can invoke the streaming form for incremental output.
- **`deterministic`** — given identical inputs (including the source file's content hash), the entry produces identical outputs. Caller can safely cache the result keyed by `req.Hash()` plus the source hash. Non-deterministic entries (random sampling, synth without a seed) must not be cached as if they were stable.
- **`expensive`** — worth caching aggressively. Cheap entries may not justify the cache machinery; expensive ones (regression, filter_to_file, profile) typically do. Hint, not a hard constraint.

`Manifest.Commands` covers CLI leaves; `Manifest.Operations` covers library-only entry points (`filter_to_file`, `process_stream`, `synth_stream`, `watch`). Embedders read both at startup to wire caching policy uniformly.
</reference>

<reference>
## Overlay streamability cross-reference

`types.OverlayStreamability` is the single source of truth for whether an overlay kind can ride the streaming Process path or forces the orchestrator down a buffered route. `descriptor.OverlayCapabilities()` reflects it as `Buffered = !streamable` on the manifest's `Overlays` block.

| Kind | Host | Streamable | Accumulator | Notes |
|---|---|---|---|---|
| `OVERLAY_INDEX_VS_TOTAL` | grouped Process (SERIES) | yes | one `f64` grand total | E3-S2. Implicit-grand-total; folds in the streaming pass alongside the per-group accumulators. |
| `OVERLAY_SHARE_OF_TOTAL` | grouped Process (SERIES) | yes | shared `f64` grand total | E3-S3. Sibling of `OVERLAY_INDEX_VS_TOTAL`; a Request carrying BOTH folds the grand total ONCE. MATRIX dispatch against a crosstab host is buffered — see `skills/crosstab-guide.md`. |
| `OVERLAY_ZSCORE_VS_TOTAL` | grouped Process (SERIES) | yes | three `f64`s (Welford count + mean + M2) | E3-S5. Population variance over N present groups (divide by N, not N-1). Folds Welford over GROUPS, not raw records. |
| `OVERLAY_INDEX_VS_PRIOR` | grouped Process (SERIES) | yes | one `f64` lag carrier per group | E4-S4. Only streamable kind in the windowed-Process family — see `skills/window-operations.md` for the full windowed catalog (`OVERLAY_DELTA_VS_BASELINE`, `OVERLAY_INDEX_VS_BASELINE`, `OVERLAY_INDEX_VS_ROLLING_MEAN`, `OVERLAY_YOY`, `OVERLAY_ZSCORE_VS_ROLLING` are buffered today). |
| `OVERLAY_DELTA_VS_SIBLING` | grouped Process (SERIES) | no (buffered) | — | E3-S7. Sibling resolution needs the finalised SeriesPayload before the `(Field, Value)` lookup runs. |
| `OVERLAY_INDEX_VS_SIBLING` | grouped Process (SERIES) | no (buffered) | — | E3-S7. Same buffered constraint as `OVERLAY_DELTA_VS_SIBLING`. |
| Every Crosstab-host overlay (`OVERLAY_INDEX_VS_MARGIN`, share triad, margin-comparison family, χ² / Fisher) | crosstab (MATRIX) | no (buffered) | — | The host crosstab path is always buffered — margins are recomputed from raw rows. The fused crosstab path falls back to buffered when `Request.Overlays` is non-empty. |

`OverlayStreamability` is map-driven — unknown kinds fall through to `false` so a missed table edit cannot accidentally let an unknown kind stream. `TestStreamability_OverlaysKnown` and `TestSkillsCoverAllOverlayKinds` enforce that every catalog entry carries a streamability row and a skill mention.

### Windowed-Process overlays

`OVERLAY_INDEX_VS_PRIOR` (E4-S4) is the only streamable kind that landed in the E4 windowed-Process family. Its single-state lag carrier is one `f64` per group, advanced on every emit during the streaming Process fold so the streaming-Process hot path stays untouched — the post-host finalize is a single divide per group. The five remaining windowed kinds (`OVERLAY_DELTA_VS_BASELINE`, `OVERLAY_INDEX_VS_BASELINE`, `OVERLAY_INDEX_VS_ROLLING_MEAN`, `OVERLAY_YOY`, `OVERLAY_ZSCORE_VS_ROLLING`) require materialised host state (positional baseline lookup, ring buffer + Welford trio, exact-key prior-year lookup) and run buffered today. See `skills/window-operations.md` for the per-kind windowed recipes and the buffered-vs-streamable rationale per kind.

### Mixed-mode downgrade rule

When a single Request carries one streamable overlay and one buffered overlay, the WHOLE Request runs buffered. `processing.canStreamOverlays` (consulted inside `Processor.canStream`) short-circuits to `false` when any spec in `Request.Overlays` is non-streamable, mirroring how `AGG_MEDIAN` forces the whole streaming pass into the buffered orchestrator (see `skills/aggregation-guide.md` → "Aggregator quirks"). Unknown overlay kinds force buffered too so the runtime surfaces `PULSE_OVERLAY_KIND_UNKNOWN` from the buffered exit. This keeps the runtime equivalence test surface byte-stable — a Request never partially-streams.

A practical implication for the windowed family: a Request that pairs `OVERLAY_INDEX_VS_PRIOR` (streamable) with `OVERLAY_INDEX_VS_ROLLING_MEAN` or `OVERLAY_YOY` (buffered) downgrades the whole Request to buffered — call `pulse predict --json` to confirm the streamability classification before pricing the call. The same rule applies to mixing the E3 streamable trio (`OVERLAY_INDEX_VS_TOTAL`, `OVERLAY_SHARE_OF_TOTAL` SERIES dispatch, `OVERLAY_ZSCORE_VS_TOTAL`) with any buffered overlay.

The implication for caching: a Request whose hash carries any buffered overlay should be priced as buffered for caching policy regardless of which streamable overlays accompany it. `Manifest.Operations["process_stream"].annotations.streamable = true` describes the entry-point capability, not the per-request decision.

For per-kind recipes against a grouped Process host see `skills/aggregation-guide.md` ("Overlays" section); for the windowed-Process family (`OVERLAY_INDEX_VS_PRIOR` + the five buffered siblings) see `skills/window-operations.md` ("Windowed-Process overlays" section); for Crosstab-host recipes see `skills/crosstab-guide.md` ("Overlays" section); for the general overlay framework see `skills/overlay-system.md`.
</reference>

<reference>
## Chain overlays

`ChainRequest` carries **two** overlay slots — one per stage on `Stages[i].Request.Overlays`, one whole-chain on `ChainRequest.Overlays`. Both slots run independently and land in different places on `ChainResponse`. Per-stage overlays fall out of E3's generic `Request.Overlays` path — no chain-specific code, the stage's own overlay handlers run at the per-stage exit before the next stage receives its synthesised cohort. Whole-chain overlays run AFTER every stage finalises (`service.ProcessChain`'s post-stage-loop barrier) and decorate any stage's already-materialised result.

### Per-stage recipe

Per-stage overlays piggyback on the existing `Request.Overlays` slot — the chain layer is transparent. Layer lands on `ChainResponse.Stages[0].Overlays`; no chain-specific code.

```json
{
  "cohort": {"path": "sales-2025.pulse"},
  "stages": [
    {
      "name": "by_region",
      "request": {
        "aggregations": [{"type": "AGG_SUM", "field": "revenue"}],
        "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
        "overlays": [
          {"kind": "OVERLAY_INDEX_VS_TOTAL", "scope": "group"}
        ]
      }
    },
    {
      "name": "top3",
      "request": {"sort": {"field": "revenue", "limit": 3}}
    }
  ]
}
```

### Whole-chain recipe

Whole-chain overlays use `ChainRequest.Overlays` with `StageRef`-shaped `Ref` (baseline) + `Target` (decorated stage). Layers land on `ChainResponse.Overlays` in matching index order, NOT on any individual `Stages[i].Overlays`. Two whole-chain kinds today: `OVERLAY_INDEX_VS_STAGE` (ratio `target/ref * 100`) and `OVERLAY_DELTA_VS_STAGE` (subtraction `target - ref`). When `Target` is omitted entirely (both `Index: nil` and `Name: ""`), the resolver defaults to the latest stage (`len(Stages) - 1`); `Ref` has no default — every spec MUST name a baseline stage explicitly.

```json
{
  "cohort": {"path": "sales-2025.pulse"},
  "stages": [
    {"name": "raw",   "request": {"aggregations": [{"type": "AGG_SUM", "field": "revenue"}], "groups": [{"type": "GROUP_CATEGORY", "field": "region"}]}},
    {"name": "filter","request": {"filterers": [{"type": "FILTER_INCLUDE", "field": "active", "values": ["true"]}]}},
    {"name": "final", "request": {"sort": {"field": "revenue"}}}
  ],
  "overlays": [
    {"kind": "OVERLAY_INDEX_VS_STAGE", "ref": {"index": 0}, "target": {"index": 2}, "scope": "total"},
    {"kind": "OVERLAY_DELTA_VS_STAGE", "ref": {"name": "raw"}, "target": {"name": "final"}, "scope": "total"}
  ]
}
```

### StageRef forms

Both `Ref` and `Target` accept exactly one of `Index` (zero-based pointer into `Stages`) or `Name` (matches `ChainStage.Name` verbatim). The XOR is enforced by the E6-S7 predict-time validator — populating both AND populating neither both reject with the same `PULSE_OVERLAY_*` configuration error. Index form is the canonical resolution path; Name form exists for human-authored chain JSON where positional indexing would be brittle across stage reordering.

```json
{"ref": {"index": 0},   "target": {"index": 2}}    // index form
{"ref": {"name": "raw"},"target": {"name": "final"}} // name form
```

`Index` uses a pointer (`*int`) so the zero value `Index: 0` (meaningfully "stage 0") is distinguishable from "no index supplied". When marshalling Go-side, set `Index: &zero` for stage 0; leaving the field nil is the omitted shape.

### Gotcha: shape divergence across stages

Stages typically reshape data across boundaries — stage 0 emits a grouped table, stage 1 filters it, stage 2 might collapse it to a scalar. `_VS_STAGE` overlays only fire when the target stage's host shape MATCHES the reference stage's host shape. When they diverge (e.g. `Ref` is a series stage, `Target` is a matrix stage) the runtime handler emits a SINGLE `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` warning per spec and surfaces an empty payload inheriting the target stage's shape — the overlay is effectively a no-op, NOT a fatal error.

> **Callout — shape-divergence warning**
> `PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT` fires when `Ref` stage's host shape (scalar / series / matrix) differs from `Target` stage's host shape. Predict-time gate (E6-S7) surfaces it as an envelope warning so the caller can fix the chain before paying the runtime cost; the runtime handler (E6-S4 / E6-S5) also emits it defensively at the post-stage-loop barrier. Warning Details carry `{target_shape, ref_shape, target_index, ref_index}` so callers can distinguish a shape mismatch from a genuine `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` configuration error.

Pre-flight every chain-overlay-bearing `ChainRequest` through `pulse predict --json` (or `descriptor.ValidateChain`) before pricing it — the shape-divergence gate runs without re-executing the chain. For per-code prose: `pulse errors lookup PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT`.

### Cross-references

- `skills/overlay-system.md` — `OVERLAY_INDEX_VS_STAGE` / `OVERLAY_DELTA_VS_STAGE` catalog rows, shared `indexKernel` / `deltaKernel` semantics, shape-inheritance rules.
- CLAUDE.md "Execution modes" → ProcessChain — source-rooted linear chain mechanics, `CanChainRequest` mergeable gate, `PULSE_CHAIN_NOT_MERGEABLE` fallback path.
- `skills/contributor-workflow.md` — `pulse api process-chain` CLI surface; the `--echo-request` per-stage normalised echo on `envelope.request`.
</reference>
