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
## Field types (all 13)

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
</reference>
