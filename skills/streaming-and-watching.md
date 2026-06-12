---
name: streaming-and-watching
description: Build reactive consumers on top of Pulse — Request.Hash() for cache keys, StreamResult[T] for incremental output, Watch/WatchDir for file-change observation, FilterToFileWithRequest for deterministic derived cohorts, and manifest CommandAnnotations for caching policy. Use when wiring Pulse into long-running services, building materialization caches, or reacting to .pulse file mutations.
type: guide
applies_to: process, compose, predict, manifest
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
    Sequence int          // Monotonic 0-based
    Data     T
    Progress float64      // 0.0–1.0, or -1 if unknown
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
