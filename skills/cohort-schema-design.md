---
name: cohort-schema-design
description: Pick the right .pulse field type — u4/u8/u16/u32/u64, f32/f64, decimal128, categorical_u8/u16/u32, packed_bool, date. Mark any field nullable to opt into the per-record null bitmap. Use when designing a schema, evaluating storage layout, or choosing nullability and bit-packing tradeoffs.
type: guide
applies_to: inspect, predict
---

# Cohort Schema Design

<skill_overview>
Schema design determines storage layout, encoding width, and downstream aggregation behavior for a `.pulse` cohort. Invoke this skill when authoring or reviewing a schema template, picking field types, planning bit-packed runs, or deciding which fields carry nulls.
</skill_overview>

<reference>
## Field types (all 17)

| Type | Byte | Notes |
|---|---|---|
| `u4` | 0 | Bit-packed 4-bit unsigned (0..15) |
| `u8` | 1 | Unsigned 8-bit (0..255) |
| `u16` | 2 | Unsigned 16-bit (0..65,535) |
| `u32` | 4 | Unsigned 32-bit (0..~4.29B) |
| `u64` | 8 | Unsigned 64-bit |
| `f32` | 4 | IEEE 754, ~7 significant digits |
| `f64` | 8 | IEEE 754, ~15 significant digits |
| `date` | 4 | Epoch days since Unix epoch (1970-01-01), no time component |
| `packed_bool` | 0 | Bit-packed boolean (1 bit) |
| `categorical_u8` | 1 | Dictionary-encoded, ≤256 entries |
| `categorical_u16` | 2 | Dictionary-encoded, ≤65,536 entries |
| `categorical_u32` | 4 | Dictionary-encoded, ≤~4.29B entries |
| `decimal128` | 16 | Fixed-point exact decimal, per-field (precision, scale) |
| `set_u8` | 1 | Multi-select bitmask over a shared dictionary (≤8 labels). Bit `i` = label `dict[i]` selected. |
| `set_u16` | 2 | Multi-select bitmask, ≤16 labels. |
| `set_u32` | 4 | Multi-select bitmask, ≤32 labels. |
| `set_u64` | 8 | Multi-select bitmask, ≤64 labels. |

Nullability is orthogonal to type. Any field may carry `Nullable: true` in its schema descriptor — see the "Null bitmap" reference below for the per-record bitmap layout that turns on when at least one field opts in.
</reference>

<reference>
## Null bitmap

Every schema field has a `Nullable bool` flag (1 byte on disk per field, alongside the type byte). When at least one field in a schema is marked `Nullable: true`, every record carries a trailing null bitmap of `ceil(field_count / 8)` bytes after the payload bytes. Bit ordering: field index `i` lives in byte `i / 8`, bit `i % 8` (LSB-first within each byte). `1` = null, `0` = present. When no field is nullable, the bitmap is absent and records use the legacy fixed-stride path with zero overhead.

The bitmap is the sole null-tracking mechanism. There is no per-type inline sentinel for nulls — a `decimal128` value of all-zero bits is the canonical decimal zero, not a null marker, and the bitmap decides which rows are null. Aggregators consult `Record.NumericValue` which routes through the bitmap; null-skip semantics for sum/mean/percentile/etc are handled centrally.

Per-record stride is `payload_bytes + ceil(field_count / 8)` when `Schema.HasBitmap()` is true, else `payload_bytes`. `Schema.RecordByteSize()` already includes the bitmap when applicable.
</reference>

<reference>
## Backwards compatibility

This is a pre-release clean break — there is no compatibility with files written by earlier Pulse binaries. The format version byte stays at `0x01`; the schema descriptor format changed (added 1-byte Nullable flag per field), so old files fail loud at schema parse with `ENCODING_INVALID`. Recreate datasets through the import / synth path against the new binary.
</reference>

<reference>
## Type selection heuristics

- Counts and IDs: pick the smallest unsigned width that fits the maximum value (`u4` < `u8` < `u16` < `u32` < `u64`).
- Floats: prefer `f32` for measurements where ~7 significant digits suffice; use `f64` for financial math, computed scores, or wide dynamic range.
- Booleans: `packed_bool`. Add `Nullable: true` if some rows can be missing the value.
- Small ordinals with missing values (Likert 1-5, grades): `u4` + `Nullable: true`.
- Calendar dates: `date`. For sub-day timestamps, store as `u64` microseconds.
- Strings: always categorical. Pick the width by expected distinct cardinality.
- Any numeric column with sometimes-missing values: pick the base type by range, then add `Nullable: true`. The bitmap costs 1 bit per field per row, paid only when the schema has at least one nullable field.
</reference>

<reference>
## Categorical width selection

| Distinct values | Width |
|---|---|
| ≤ 256 | `categorical_u8` |
| ≤ 65,536 | `categorical_u16` |
| up to ~4.29B | `categorical_u32` |

Exceeding the chosen width raises `PULSE_IMPORT_CATEGORICAL_OVERFLOW`; an unbounded inferred dictionary raises `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`.
</reference>

<reference>
## Set columns (multi-select bitmasks)

Set-typed columns model "the respondent picked several values from a shared catalog" — for example a survey field listing which credit-card issuers a respondent holds. The payload is a fixed-width unsigned integer; bit `i` is set when the dictionary's `i`-th entry was selected. The dictionary lives inline in the schema block (same encoding as `categorical_*`), so every shard's records reference a known label list and bitwise operations (`|` = union, `&` = intersection, `^` = symmetric difference) work uniformly.

| Distinct labels | Width | On-wire bytes |
|---|---|---|
| ≤ 8 | `set_u8` | 1 |
| ≤ 16 | `set_u16` | 2 |
| ≤ 32 | `set_u32` | 4 |
| ≤ 64 | `set_u64` | 8 |

An empty mask (`0x00`) is a valid value meaning "no selection" — it is NOT a null. Null state lives in the per-record bitmap as for any other field.

Shard archives merge set dictionaries with the same union semantics as categoricals; per-record bitmasks are rewritten with the bit-permutation matrix derived from the dict remap. Width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW` at insert time. Importer cardinality overflow → `PULSE_IMPORT_SET_OVERFLOW`.

`Record.SetValue(name) (uint64, bool)` returns the raw mask through `Record.wide` so set_u64's high bits survive (float64 precision footgun avoided). `Record.SetLabels(name)` resolves the mask to a sorted `[]string` of dictionary labels; `Record.AllValues()` does the same for the expression environment so `"VISA" in card_issuers` works inside `FILTER_EXPRESSION` and `ATTR_FORMULA`.

The dedicated operator surface is six aggregators (`AGG_SET_*`), four filterers (`FILTER_SET_*`), two groupers (`GROUP_SET_*`), and two attributes (`ATTR_SET_*`); see the aggregation-guide, grouper-design, and attribute-composition skills for details. Smart defaults pair `AGG_SET_FREQUENCY` with `GROUP_SET_PER_ELEMENT` so "respondents per option" answers without further configuration.

### Importing set columns

The inference engine classifies a column as `set_*` when the **delimited-cell heuristic** fires:

1. At least `pulse.Options.SetInferenceMinPct`% of non-null sampled cells contain the inferred delimiter (default 30; the threshold is also exposed per-import via `imports.Spec.SetInferenceMinPct`).
2. Post-split unique tokens fit `set_u64` (≤ 64). Larger cardinalities fall through to `categorical_*`.
3. Average post-split cardinality is > 1 (rules out columns that occasionally carry a delimiter inside a free-text categorical string).

The delimiter is probed in priority order — `|` first, `;` second. Comma is intentionally absent because a comma-delimited cell inside CSV is unparseable through the CSV reader. When the heuristic fires, the chosen delimiter is cached per column on the `ImportJob` so the row pass can split consistently; explicit-schema imports default to `|`.

`Parquet` / `Arrow` files with `LIST<UTF8>` columns are recognised natively: `TypeToPulse(arrow.LIST)` returns `set_u8` as the initial guess, and `FormatValue` joins the list elements with `|` so the downstream import path treats them like delimited strings. `NDJSON` and JSON Array files allow scalar arrays at the top level (e.g. `"tags": ["a", "b"]`); arrays-of-objects / arrays-of-arrays still raise a typed parse error.

When the heuristic misfires, override with the **force-type sidecar**:

```json
// imports/<handle>.pulse.sidecar.json
{
  "handle": "...",
  "column_type_overrides": {
    "issuers": "set_u8",
    "tags": "categorical_u8"
  }
}
```

The sidecar override skips inference for the named columns and pins the column type; the dictionary is still built from observed sample values during the row pass. Unknown FieldType names raise `SERVICE_VALIDATION` at sidecar load so misconfigurations never reach the codec. Programmatic callers can pass `imports.Spec.ColumnTypeOverrides` directly to `Pulse.Import` to inject the same intent without writing the sidecar by hand.
</reference>

<rule severity="should" topic="bit-packing">
## Bit-packing rules

- `packed_bool` and `u4` return `ByteSize() == 0` and share bytes with adjacent packed fields. Use `FieldType.IsBitPacked()` to detect.
- The encoder coalesces a run of consecutive packed fields into the minimum number of bytes; place packed fields next to each other for optimal layout.
- Reordering schema fields can change byte offsets even when types are unchanged.
- The trailing null bitmap (when present) is appended after the payload — bit-packed neighbours still share their payload bytes, and the bitmap occupies its own dedicated trailing bytes.
</rule>

<rule severity="must" topic="descriptions">
## Descriptions

- Capped at 1000 bytes per field; longer values raise `PULSE_IMPORT_DESCRIPTION_TOO_LONG`.
- Empty, sub-10-character, or generic descriptions ("n/a", "tbd", "unknown", "field", "data", "value", "column") trigger `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (warning by default, error under `--strict`).
- Style: concise, third-person, present-tense; state what the field represents, its units, and any domain semantics.
</rule>

<reference>
## Schema-template workflow

Import is a CLI / library operation; there is no `pulse_import` MCP tool today. Point a human at https://frankbardon.github.io/pulse/cli/cohort-inspect.html plus the import chapters in mdBook for the `schema-template` -> edit -> import flow.
</reference>

<reference>
## Inspect post-import

Call `pulse_inspect` with `{"path": "FILE.pulse"}` to verify field types, byte offsets, descriptions, and (truncated) categorical dictionaries. The MCP handler reads only the header — it is cheap regardless of cohort size.
</reference>

<reference>
## Sharded cohorts

A `.pulse` path can resolve to either of two shapes:

1. **Single-file cohort.** The byte format documented above — 9-byte `PULSE\x00\x00\x00\x01` header, schema, dictionaries, records. First four bytes are `PULSE`.
2. **Shard archive.** An uncompressed Zip64 archive (Method 0, store-only) whose first four bytes are the zip magic `PK\x03\x04`. The archive contains one reserved `_schema.pulse` entry plus one or more shard payloads. Each shard payload is a complete, standalone single-file `.pulse` (same byte layout as #1).

The two shapes are dispatched at `pulse.Open` time on the leading magic. Old readers that handle only the single-file layout fail loud at the magic check on an archive — correct fail-loud behavior, not silent corruption.

### Archive layout

```
FILE.pulse                              uncompressed Zip64, magic PK\x03\x04
  ├─ _schema.pulse                      reserved name: header-only canonical schema + SHRD trailer
  ├─ 20190101.pulse                     shard (standalone single-file .pulse)
  ├─ 20190108.pulse                     shard
  └─ ...
```

The `_schema.pulse` entry is a header-only Pulse file (zero records) carrying the canonical schema block plus a `SHRD` trailer:

- `aggregate_record_count uint64` — sum across all shards, cached so `pulse inspect` does not have to crack every shard header.
- `shard_count uint16` — redundant with the central directory count, sanity check.

### Schema cohesion

Structural cohesion is **strict** at shard insert (`pulse shard add`). The incoming shard's header is compared byte-equally against the canonical schema in `_schema.pulse`:

- Field count must match.
- For every field: name, type byte, byte_offset, bit_position must match.
- For `categorical_*` fields: the type width (u8 / u16 / u32) is fixed at archive creation.

Mismatch raises `PULSE_SHARD_SCHEMA_MISMATCH` and the insert is rejected.

Field descriptions are **tolerant**: divergence across shards emits `PULSE_SHARD_DESCRIPTION_DIVERGENCE` as a warning (not an error). The canonical description carried in `_schema.pulse` wins for any downstream consumer.

### Dictionary growth — union merge

Categorical dictionaries are malleable across shards under union-merge semantics. At shard insert (`CreateShardArchive` / `AddShard`), for each `categorical_*` field the runtime computes the **union** of the canonical and incoming dictionaries: canonical entries first in their existing order, then any new entries from incoming in their order. The canonical `_schema.pulse` adopts the union; if the incoming shard's dict indices differ from the canonical (union) indices, the incoming shard's record bytes are rewritten with remapped categorical indices before being placed in the archive. Indices are stable across reads — record bytes always reference the canonical dictionary inside the archive.

Width overflow: a union that would exceed the declared categorical width (256 entries for `categorical_u8`, 65,536 for `categorical_u16`, 2³² for `categorical_u32`) raises `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. The archive must be rebuilt with a wider categorical type. **Mitigation:** pick categorical widths with growth headroom at archive creation.

Pulse provides a stricter prefix-only validator (`encoding.ValidateDictPrefixRule` / surfaced through `pulse shard verify`) for callers that want to fail on divergence instead of merging — useful for archives whose embedders coordinate dictionaries upstream and want corruption detection.

### Memory shape

Operations that materialize the entire input — percentile/median aggregators, `ATTR_PERCENTILE`, `GROUP_QUANTILE`, `GROUP_DATE`, window operators, decimal paths, tier-1 tests combined with groupers/features/two-pass attrs, tier-2 post tests — materialize across the **union** of shards, not per-shard. This is mathematically required for global percentile semantics (median-of-medians is not the median).

Memory cost scales with shard count. A 13-week quarterly archive costs roughly 13× the single-shard buffer for these ops. Pick shard granularity with this multiplier in mind. Embedders that need percentile across very large archives should keep individual shards smaller and accept the streaming cost, or pre-aggregate into a single coarser shard.

### Anchor syntax

A specific shard inside an archive is addressable via the `#` anchor:

```
respondents/Q1_2019.pulse                    → opens the full sharded cohort (union semantics)
respondents/Q1_2019.pulse#20190101.pulse     → opens just the named shard as a one-shard cohort
```

The anchor is parsed by `pulse.Open`. Anchor-referenced shards participate in the union when the archive is opened plainly, and they are valid standalone cohorts when the anchor is used. Anchor against a single-file `.pulse` raises `PULSE_ARCHIVE_MAGIC_INVALID`; missing anchor raises `PULSE_SHARD_MISSING`.

### Concurrency

Pulse does **not** provide concurrent-writer protection. Two processes running `pulse shard add` against the same archive race; last writer wins, earlier writer's shard is lost. Concurrency is the caller's responsibility. Recommended patterns:

- Single-writer architecture (one process owns mutations; readers are unconstrained).
- External advisory lock (flock, orchestrator coordination).

Read-side concurrency is safe — readers snapshot the central directory and `_schema.pulse` at `Open` time and never re-read. A concurrent `shard add` does not affect an already-open reader; re-open to see new shards.

## Parallel decode

The buffered Process path on a single-file (non-shard) cohort can fan out record-decode across a worker pool when the request is mergeable. This is the E3 surface of the crosstab-perf rollout, controlled by `pulse.Options.DecodeWorkers` (see CLAUDE.md "Build / Env" for the knob, defaults, and rejection of negative values). It is **orthogonal to `ShardWorkers`** — shard archives parallelise across shards via `service/shard_reduce.go`; this path parallelises across record segments within a single file. The two never stack: a shard-archive cohort is gated out below.

### When parallel decode engages

Every condition in `service.canParallelDecode` must hold (this is the single integration site Process consults):

- `DecodeWorkers != 1` — the caller has not forced strictly serial.
- `recordCount >= service.parallelDecodeRecordThreshold` (currently `100_000`) — below this the worker spawn + state-merge overhead dominates the segment win. Record count is taken from the header-fast `pulse.CountRecords` path, never a payload slurp, so a bail on threshold pays no extra I/O.
- The cohort is single-file (`len(cohort.Shards()) == 0`) — shard archives use `ShardWorkers` instead; stacking would double-spawn workers on the same CPUs.
- `resolveRealPath` returns a real on-disk path — mmap is required to share the record region read-only across workers. `MemMapFs`, the anchor overlay, and custom `afero.Fs` implementations without `RealPather` all bail here.
- `processing.CanMergeRequest(req, schema)` is true — every operator in the request must support associative merge of per-worker partial state. The same gate the per-shard reducer uses (built-ins only; non-decimal targets; no windows/features/regressions/tests/two-pass; aggregator emits scalar).

Any bail returns a short stable reason string (e.g. `"below threshold (50000 < 100000)"`, `"mmap unavailable (no RealPath)"`, `"non-mergeable request"`) suitable for debug logs or future telemetry. Process falls through to the serial `scanIter` path with byte-equal output.

### Why crosstab is NOT exercised today

Crosstab requests fail `CanMergeRequest`. `validateCrosstabSpec` requires `req.Aggregations` to be empty (cell aggregation lives in `Crosstab.Cell` instead), but `CanMergeRequest` requires `len(req.Aggregations) > 0`. The infrastructure here is therefore dormant on the canonical crosstab-perf benchmark (`tmp/huge-request.json`) and exists to benefit **future non-crosstab buffered Process calls** — wide cohorts with a mergeable request slate. E3 ships parallel decode as orthogonal plumbing alongside the crosstab-perf headline epics (projection, decode plans), not as a crosstab speedup.

### How segments are chosen

`parallelDecodeMmap` partitions the mmap'd record region into `workers` contiguous ranges at stride-aligned boundaries (`Schema.RecordByteSize()`). The first `workers-1` segments each get `floor(totalRecords / workers)` records; the last segment absorbs the remainder so the partition covers exactly `[0, totalRecords)`. Each worker constructs its own `bytes.Reader` over its sub-slice of the shared mmap, walks records via `RecordReader.ReadRecordWithWidePlan` (when the same projected `DecodePlan` Process would have installed on the serial path is non-nil) or `ReadRecordWithWide` otherwise, and fires its per-record callback. Cancellation is polled at each record boundary; the first worker to return a non-nil error propagates through the errgroup and cancels the rest.

Bit-packed neighbours can never straddle a segment boundary because record stride is byte-aligned per the byte-layout invariants (`Schema.RecordByteSize` reserves one byte per bit-packed field). A defensive assertion at dispatch catches a malformed schema with a typed `PROCESSING_INTERNAL` error before fan-out.

### Mergeable-aggregator requirement

Each worker owns a `*shardPartial` — the same shape `shard_reduce.go` uses for archive-shard parallelism. Per-record callbacks update only the worker's own partial; there is no shared mutable state on the hot path. After the errgroup boundary completes, `mergeShardPartials` folds partials in **worker-index order** (which equals cohort byte order because segments are assigned contiguous record ranges), then `finalizeMergedPartial` emits the Response. Worker-index iteration order matters for the Welford-Pébaÿ AGG_MEAN / AGG_STDDEV / AGG_VARIANCE merge: Chan-Welford is associative but not strictly commutative when partition sizes differ, so floating-point byte equality across runs depends on stable order.

Adding a new aggregator that should compose under this path means implementing `MergeableAggregator.Merge(other)` AND declaring `AggregationType.Mergeable() == true`. See `skills/contributor-workflow.md` for the contributor rule. Associative+commutative aggregators (count, sum, min, max, null_count, frequency, distinct_count, mode) produce byte-equal results vs the serial path; Welford mean / variance / stddev drift within ULP via Chan-Welford (the same guarantee the shard reducer ships).

### Threshold rule

`parallelDecodeRecordThreshold = 100_000` records. Below this the buffered Process path stays serial regardless of `DecodeWorkers`. The constant is colocated with the gate (`service/service.go`) and the worker-count resolution (`shouldFanOutDecode`); changing it touches both the predicate and the dispatcher together. Tests at `service/decode_workers_test.go` and `service/parallel_decode_eligibility_test.go` lock the threshold semantics; `TestProcess_ParallelDecode_BelowThresholdBails` is the canonical regression.

### Observed perf characteristics

Reference cohort: 200-field synth × 100K rows, mergeable request slate, OsFs (mmap engaged), 10-core box.

| Worker count          | Wall-clock          | Memory  | Allocs |
|-----------------------|---------------------|---------|--------|
| `DecodeWorkers=1` (serial) | ~478ms          | 25 MB   | 1.1M   |
| `DecodeWorkers=2`     | ~506ms (regression — parallel overhead) | 778 MB | 21.6M |
| `DecodeWorkers=4`     | ~363ms              | 778 MB  | 21.6M  |
| `DecodeWorkers=10` (NumCPU) | ~370ms          | 778 MB  | 21.6M  |

Observed NumCPU-vs-serial wall-clock ratio: **0.747** (~25 % win). The E3-S5 acceptance target was **0.67** (33 % win); the perf gate test (`TestParallelDecode_PerfGate`, build-tagged `perf`) currently does **not** clear that threshold. Document the honest number — do not claim the 33 % win the code does not yet deliver.

**Root cause of the alloc explosion.** `service/service.go`'s parallel dispatcher hardcodes `projectMapHint = len(schema.Fields)` for the parallel context and only narrows when `s.projectBuffered` is true. With projection disabled, each per-worker map is sized for ALL 200 fields per record. Result: 21.6M parallel allocs vs 1.1M serial. Plumbing the projected `DecodePlan` retained-set size into `parallelDecodeContext.projectMapHint` (mirroring `streamingIterator.projectSize`) likely closes the gap and clears the 0.67 gate. This is the high-priority follow-up for the next E3 iteration.

**Workers=2 regression.** At low worker count parallel overhead (errgroup setup + per-worker state allocation + the post-Wait merge fold) dominates the segment win. Recommend `DecodeWorkers >= 3` for best results until a cost-model fan-out gate lands in `shouldFanOutDecode`. The constant-time predicate is fine; the worker-count floor is the natural next gate.

### Surface

- `service/parallel_decode.go` — `canParallelDecode` (predicate), `shouldFanOutDecode` (worker-count resolver), `parallelDecodeMmap` (segment-aware fan-out), `buildParallelDecodeContext` (mmap + cursor setup).
- `service/parallel_reduce.go` — `reduceParallelBuffered` (per-worker partial state + worker-index merge fold).
- `service/service.go` — `processSingleFileParallelMaybe` (dispatch site in `Process`, post-`shouldFanOut`-for-shards check), `SetDecodeWorkers` / `DecodeWorkers()` (service-level setter/getter), `parallelDecodeRecordThreshold` constant.
- `pulse.go` — `Options.DecodeWorkers` (public knob, validated at `New`).

CI gates: `TestProcess_ParallelDecode_ByteEqualToSerial`, `TestProcess_ParallelDecode_BelowThresholdBails`, `TestProcess_ParallelDecode_MemMapFsBails`, `TestProcess_ParallelDecode_NonMergeableBails`, `TestProcess_ParallelDecode_WorkersOneBails`, `TestCanParallelDecode_*`, `TestShouldFanOutDecode_*` (the E3-S4 matrix), plus the build-tagged `TestParallelDecode_PerfGate` (E3-S5).

## Decode plan and projection

When `pulse.Options.ProjectBufferedFields` is enabled, the streaming iterator decodes each record by walking a precomputed `encoding.DecodePlan` instead of every schema field. Lifetime is purely runtime: nothing about the `.pulse` file changes — the plan is built in-memory from `(Schema, retained set)` and dies with the iterator.

How it is built. `processing.NeededFields(req, schema, ext)` produces the retained set from the request's slots (Aggregations, Attributes, Filterers, Groups, Windows, Features, Tests, Regressions, Sort.Field, plus `Crosstab.Rows/Columns/Cell` and `Labels[].Field` on the crosstab path). The iterator hands that set to `Schema.BuildDecodePlan(retained)`, which returns a deterministic `[]Segment` of two concrete types:

- `SkipBytes{N}` — the iterator advances the underlying reader N bytes with a single `Seek` (or `io.CopyN` discard) and decodes nothing. Coalesces every contiguous run of unprojected non-bit-packed fields into one segment.
- `DecodeFields{Fields}` — the existing per-field decode loop, scoped to one group of fields.

Bit-packed grouping. `u4` and `packed_bool` neighbours form one group (each member consumes a full byte on-wire via `ReadBit` / `ReadNibble`). The group is **all-or-nothing**: if any member is retained the whole group becomes one `DecodeFields` (unretained members are still decoded to keep the cursor aligned, only their map writes are suppressed by the keep filter); if no member is retained the group folds into a single `SkipBytes{N: K}` where K is the group size.

Bitmap whole-or-skip. When `Schema.HasBitmap()` (see "Null bitmap" above) and at least one nullable field is retained, the plan appends a final `DecodeFields` carrying every nullable field in schema order — the iterator reads the bitmap once and surfaces nulls for the retained subset. When no nullable field is retained the plan appends a single `SkipBytes{N: Schema.BitmapByteSize()}` and the bitmap is never read.

Plan cache. The iterator caches the plan keyed by `(schema-pointer identity, joined sorted retained-set string)`. Re-calling `SetProjection` with the same retained set reuses the same plan; the cache lives on the iterator and is discarded when it closes. Nothing in a request's lifetime invalidates an already-built plan — the plan is a pure function of inputs.

Bench reference. On a 200-field synth cohort × 100K rows, projecting to 4 fields drops `Process` from ~1.07s to ~155ms (~7×, 14× fewer allocs) versus the per-field walk. See CLAUDE.md "Byte-layout invariants" projected-decode paragraph for the gate-relevant summary and `skills/extension-points.md` for the `FieldInputs` hook custom operators use to participate.

## Iterator mmap policy

`Process` and the other streaming facades back their record scan with a memory-mapped read when the underlying `afero.Fs` ultimately resolves to a regular on-disk file. Eligibility is decided at iterator init by `service.resolveRealPath`, which probes the fs in this order:

1. `fs` satisfies the `service.RealPather` capability interface (`interface{ RealPath(name string) (string, error) }`). Both `*afero.BasePathFs` (the default fs returned by `fs.Default()` under `PULSE_DATA_DIR`) and external opt-in wrappers — e.g. a CopyOnRead overlay that downloads `.pulse` objects from object storage into a local cache — match this layer. The returned path must be `os.Open`-able at the moment of the call.
2. `fs` is `*afero.OsFs`. The cohort path is already the on-disk path.

`MemMapFs`, `ReadOnlyFs`, and any other wrapper that doesn't advertise `RealPath` return `("", false)` and the iterator falls back to `afero.ReadFile`. Hermetic tests built on `fs.NewMemMap()` therefore stay clean — they exercise the non-mmap path automatically with no `t.Skip` required.

The probe is intentionally a fast capability check. There is no third "open-and-inspect" layer that would issue an extra `Open`+`Stat` per `Process` call to discover a real path the wrappers didn't advertise. Rationale lives inline at `service/fs_probe.go`: every fs Pulse depends on (in-tree `BasePathFs`, downstream CoR overlays) can advertise `RealPath` at near-zero cost; charging a syscall on the hot path to rescue mis-implemented wrappers would penalise the common case.

Implication for embedders: when wiring a new `afero.Fs` implementation whose virtual paths ultimately map to a regular local file, implement `RealPather` to opt that fs into the mmap fast path. See `skills/contributor-workflow.md` ("Adding a new `afero.Fs` implementation") for the contributor rule and the regression test that catches a missing capability.
</reference>
