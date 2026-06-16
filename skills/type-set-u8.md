---
name: type-set-u8
kind: type
description: 1-byte multi-select bitmask over an inline dictionary of up to 8 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 1 byte per record. Not bit-packed (the byte is consumed atomically as a bitmask, not bit-cursor-shared). Stride contributes 1 byte. Bit `i` of the byte corresponds to dictionary entry `i` — bit set means "label `i` selected".

## Range

Up to `MaxSetEntries()` = `8` distinct labels in the inline dictionary. The on-wire value is any 8-bit bitmask, so 2^8 = 256 distinct selection patterns are representable. **Empty mask (`0x00`) is a valid value meaning "no labels selected" — it is NOT null.** Overflow on import → `PULSE_IMPORT_SET_OVERFLOW`.

## Null

Orthogonal — and the empty-vs-null distinction is load-bearing. `Nullable: true` participates in the per-record null bitmap; null is signaled there alone. Mask `0x00` = "no selections present". Decimal128 and `set_*` are the two types where in-band sentinels are explicitly disallowed.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block, shared across all records; `MaxDictEntries()` = `8`. Shard cohesion: union-merge under the 8-label cap; width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Nullability + per-record bitmap, Width overflow).
- Cross-link: `op-agg-set-frequency`, `op-attr-set-has`.
