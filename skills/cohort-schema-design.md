---
name: cohort-schema-design
description: Field-type selection for .pulse cohorts — 17 types, nullability via per-record bitmap, shard archive anchor syntax, description-length cap. Use when picking schema types, evaluating storage layout, or interpreting a cohort returned by pulse_inspect.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [u4, u8, u16, u32, u64, f32, f64, decimal128, categorical_u8, categorical_u16, categorical_u32, packed_bool, date, set_u8, set_u16, set_u32, set_u64]
---

# Cohort schema design

Pick the right `.pulse` field type, decide nullability, address shards. The schema lives in the cohort header; `pulse_inspect` is the surface for reading it.

## Field-type matrix (all 17)

| Name | Bytes | Dict? | Bit-packed? | Notes |
|---|---|---|---|---|
| `u4` | 0 | no | yes | 0..15 |
| `u8` | 1 | no | no | 0..255 |
| `u16` | 2 | no | no | 0..65,535 |
| `u32` | 4 | no | no | 0..~4.29B |
| `u64` | 8 | no | no | |
| `f32` | 4 | no | no | ~7 sig digits |
| `f64` | 8 | no | no | ~15 sig digits |
| `date` | 4 | no | no | epoch days since 1970-01-01 |
| `packed_bool` | 0 | no | yes | 1 bit |
| `categorical_u8` | 1 | inline, ≤256 | no | dict-encoded |
| `categorical_u16` | 2 | inline, ≤65,536 | no | |
| `categorical_u32` | 4 | inline, ≤~4.29B | no | |
| `decimal128` | 16 | no | no | per-field `(precision, scale)` |
| `set_u8` | 1 | shared, ≤8 labels | no | multi-select bitmask |
| `set_u16` | 2 | shared, ≤16 | no | |
| `set_u32` | 4 | shared, ≤32 | no | |
| `set_u64` | 8 | shared, ≤64 | no | |

`set_*` mask bit `i` = label `dict[i]` selected; empty mask is a valid value (NOT null). Sub-day timestamps: store as `u64` microseconds. Nullability is opt-in per field via the bitmap; all 17 types participate identically.

## Selection heuristics

- Counts / IDs: smallest unsigned width that fits the max.
- Floats: `f32` for measurements; `f64` for scores or wide dynamic range.
- Money: `decimal128` — see `financial-cohorts`.
- Booleans: `packed_bool`. Small ordinals (Likert, grades): `u4`.
- Strings: always categorical. Width by distinct cardinality.
- Multi-select: `set_*`. Width by distinct-label cap.
- Sometimes-missing values: pick base type, then `Nullable: true`.

## Nullability + per-record bitmap

When at least one field is `Nullable: true`, every record carries a trailing bitmap of `ceil(field_count / 8)` bytes after the payload:

- Field index `i` → byte `i/8`, bit `i%8` (LSB-first). `1` = null, `0` = present.
- No nullable fields ⇒ no bitmap; zero-overhead path.

The bitmap is the sole null mechanism. No type has an inline sentinel — `decimal128` all-zero bits is decimal zero, not null. Null-skip semantics for sum/mean/percentile are central.

**Inferred imports promote on out-of-sample nulls.** Schema inference reads only the first `--sample-rows` (default 500) rows to decide nullability. If a null (`""`/`null`/`na`/`n/a`) lands in a column past that window, the importer promotes the field to nullable and continues rather than failing — emitting a `PULSE_IMPORT_NULL_PROMOTED` warning listing the promoted fields (also on `ImportReport.PromotedFields` / the managed `Result.promoted_fields`). This applies to every inferred text/columnar import (csv, tsv, ndjson, jsonarray, parquet, arrow, excel). An **explicit `--schema`** is a contract: a null in a field you declared non-nullable stays a `PULSE_IMPORT_ROW_ERROR`. To avoid promotion surprises, mark sometimes-missing fields `"nullable": true` in the schema, or raise `--sample-rows`.

## Bit-packed runs

`u4` and `packed_bool` report `ByteSize() == 0` and share bytes with adjacent packed fields. Place packed fields together for optimal layout. Reordering can change byte offsets even when types are unchanged.

## Width overflow

- `PULSE_IMPORT_CATEGORICAL_OVERFLOW` / `PULSE_IMPORT_CATEGORICAL_UNBOUNDED` — categorical width exceeded or dict unbounded.
- `PULSE_IMPORT_SET_OVERFLOW` — `set_*` cardinality exceeded width.
- `PULSE_SHARD_DICT_WIDTH_OVERFLOW` — shard insert would expand union dict past declared width.

Mitigation: pick widths with growth headroom up front.

## Field descriptions

- Capped at 1000 bytes per field. Over-cap → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`.
- Empty, sub-10-character, or generic ("n/a", "tbd", "unknown", "field", "data", "value", "column") → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (warning; error under strict).
- Style: concise, third-person, present-tense; state what the field represents, its units, and domain semantics.

## Sharded cohorts

A `.pulse` path resolves to one of two shapes; dispatch on the leading 4 bytes:

1. **Single-file.** Magic `PULSE\x00\x00\x00` + format byte `0x01`. Schema, dicts, records.
2. **Shard archive.** Uncompressed Zip64 (Method 0); magic `PK\x03\x04`. Reserved `_schema.pulse` (header-only canonical schema + `SHRD` trailer with `aggregate_record_count` + `shard_count`) plus N standalone shard payloads.

Old single-file readers fail loud on archive magic.

### Cohesion

- Structural: **strict** at insert. Field count, names, type bytes, byte offsets, bit positions must match canonical. Mismatch → `PULSE_SHARD_SCHEMA_MISMATCH`.
- Descriptions: **tolerant**. Divergence → `PULSE_SHARD_DESCRIPTION_DIVERGENCE` (warning); canonical wins.
- Categorical / set dictionaries: union-merge. Canonical entries first; new entries appended; incoming records byte-rewritten with remapped indices.
- Stricter prefix-only validator raises `PULSE_SHARD_DICT_DIVERGENCE` when embedders coordinate dicts upstream.

### Anchor syntax

```
respondents/Q1.pulse                 → full sharded cohort (union semantics)
respondents/Q1.pulse#20190101.pulse  → named shard, as one-shard cohort
```

Anchor against single-file → `PULSE_ARCHIVE_MAGIC_INVALID`. Missing anchor → `PULSE_SHARD_MISSING`.

### Memory shape

Materializing ops (percentile / median aggs, `ATTR_PERCENTILE`, `GROUP_QUANTILE`, `GROUP_DATE`, windows, decimal paths, tier-1 tests + groupers/features/two-pass attrs, tier-2 post tests) materialize across the **union** of shards. Median-of-medians is not the median. Memory scales with shard count.

### Concurrency

No concurrent-writer protection. Two writers race; last writer wins. Readers snapshot at open. Caller owns single-writer architecture or external advisory lock.

## Cross-links

- `financial-cohorts` — `decimal128` rules.
- `response-components` — `data.components.run.shard_count` + `partial_cohort_reason`.
- `aggregation-guide` / `grouper-design` / `attribute-composition` — `set_*` operator surfaces.
