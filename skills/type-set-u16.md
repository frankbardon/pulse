---
name: type-set-u16
kind: type
description: 2-byte multi-select bitmask over an inline dictionary of up to 16 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 2 bytes per record, little-endian. Not bit-packed (the value is consumed atomically as a bitmask). Stride contributes 2 bytes. Bit `i` of the 16-bit value corresponds to dictionary entry `i` — bit set means "label `i` selected".

## Range

Up to `MaxSetEntries()` = `16` distinct labels in the inline dictionary. 2^16 = 65,536 distinct selection patterns are representable on the wire. **Empty mask (`0x0000`) is a valid value meaning "no labels selected" — it is NOT null.** Overflow on import → `PULSE_IMPORT_SET_OVERFLOW`.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap; null is signaled there alone. Mask `0x0000` = "no selections present", distinct from null. In-band sentinels are explicitly disallowed for `set_*` types.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block, shared across all records; `MaxDictEntries()` = `16`. Shard cohesion: union-merge under the 16-label cap; width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. An SPSS multiple-DICHOTOMY response set imports as one of these, with the constituent variables' field names as the entries — additively, beside the constituent columns themselves (`cohort-schema-design`, SPSS import).

## See

- Skill: `cohort-schema-design` (Field-type matrix, Nullability + per-record bitmap, Width overflow).
- Cross-link: `op-agg-set-frequency`, `op-attr-set-has`.
