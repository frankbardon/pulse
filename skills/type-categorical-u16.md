---
name: type-categorical-u16
kind: type
description: 2-byte categorical with inline dictionary up to 65,536 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 2 bytes per record (dictionary index, little-endian). Not bit-packed. Stride contributes 2 bytes to `Schema.RecordByteSize`. The inline dictionary block lives in the schema.

## Range

`MaxCategoricalEntries()` = `65,536` distinct labels. Right-sized for medium-cardinality enums — city, product SKU within a department, vendor codes. Overflow on import → `PULSE_IMPORT_CATEGORICAL_OVERFLOW`; shard insert past the width → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. Index `0` is the first dictionary entry, never null. `IsCategorical()` is `true`; default aggregator is `AGG_FREQUENCY`, default grouper is `GROUP_CATEGORY`.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block; `MaxDictEntries()` = `65,536`. Shard cohesion: union-merge — incoming records byte-rewritten with remapped indices when canonical dict diverges.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Width overflow, Sharded cohorts — Cohesion).
- Cross-link: `type-set-u16` for multi-select up to 16 distinct labels.
