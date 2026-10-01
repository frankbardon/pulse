---
name: type-categorical-u8
kind: type
description: 1-byte categorical with inline dictionary up to 256 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 1 byte per record (dictionary index, little-endian when read via the wider helpers). Not bit-packed. Stride contributes 1 byte to `Schema.RecordByteSize`. The inline dictionary block lives in the schema, not in each record.

## Range

`MaxCategoricalEntries()` = `256` distinct labels. Use for narrow string enums: country code, status, response option, segment label. Overflow on import → `PULSE_IMPORT_CATEGORICAL_OVERFLOW`; shard insert that would expand union dict past the width → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. Pick the width with growth headroom.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. The byte `0` is the first dictionary entry, never null. `IsCategorical()` is `true`; default aggregator is `AGG_FREQUENCY`, default grouper is `GROUP_CATEGORY`.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block; `MaxDictEntries()` = `MaxCategoricalEntries()` = `256`. Shard cohesion: union-merge — incoming records byte-rewritten with remapped indices when canonical dict diverges.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Width overflow, Sharded cohorts — Cohesion).
- Cross-link: `type-set-u8` for multi-select.
