---
name: type-categorical-u32
kind: type
description: 4-byte categorical with inline dictionary up to ~4.29B labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record (dictionary index, little-endian). Not bit-packed. Stride contributes 4 bytes to `Schema.RecordByteSize`. The inline dictionary block lives in the schema.

## Range

`MaxCategoricalEntries()` = `4,294,967,295` (~4.29B) distinct labels. The widest categorical — surrogate keys with labels, very large enums, free-text canonicalized columns. Practical dictionary size is bounded by the schema-block budget; reach for `u32` (no dictionary) when labels offer no analytic value. Overflow on import → `PULSE_IMPORT_CATEGORICAL_OVERFLOW`; shard insert past the width → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. Index `0` is the first dictionary entry, never null. `IsCategorical()` is `true`; default aggregator `AGG_FREQUENCY`, default grouper `GROUP_CATEGORY`.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block; `MaxDictEntries()` = `4,294,967,295`. Shard cohesion: union-merge — incoming records byte-rewritten with remapped indices on dict divergence.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Width overflow, Sharded cohorts — Cohesion).
- Cross-link: `type-set-u32`, `type-u32`.
