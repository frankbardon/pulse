---
name: type-set-u32
kind: type
description: 4-byte multi-select bitmask over an inline dictionary of up to 32 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian. Not bit-packed (the value is consumed atomically as a bitmask). Stride contributes 4 bytes. Bit `i` of the 32-bit value corresponds to dictionary entry `i` — bit set means "label `i` selected".

## Range

Up to `MaxSetEntries()` = `32` distinct labels in the inline dictionary. 2^32 selection patterns are representable on the wire. **Empty mask (`0x00000000`) is a valid value meaning "no labels selected" — it is NOT null.** Overflow on import → `PULSE_IMPORT_SET_OVERFLOW`.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap; null is signaled there alone. Empty mask is distinct from null. In-band sentinels are explicitly disallowed for `set_*` types.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block, shared across all records; `MaxDictEntries()` = `32`. Shard cohesion: union-merge under the 32-label cap; width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Nullability + per-record bitmap, Width overflow).
- Cross-link: `op-agg-set-frequency`, `op-attr-set-has`, `type-categorical-u32`.
