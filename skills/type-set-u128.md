---
name: type-set-u128
kind: type
description: 16-byte multi-select bitmask over an inline dictionary of up to 128 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 16 bytes per record, little-endian **word** order — `words[0]` holds bits 0..63 and occupies the lowest 8 bytes. Not bit-packed (the value is consumed atomically as a bitmask). Stride contributes 16 bytes. Bit `i` corresponds to dictionary entry `i` — bit set means "label `i` selected". Too wide for the `uint64` scalar value API: `encoding.ReadFieldValue` / `WriteFieldValue` REFUSE it with `ENCODING_TYPE_MISMATCH` rather than silently truncate to the low 64 bits. `FieldType.IsWideSet()` names that split for callers that must branch; wide masks travel as `SetMask`.

## Range

Up to `MaxSetEntries()` = `128` distinct labels in the inline dictionary. **Empty mask is a valid value meaning "no labels selected" — it is NOT null.** Overflow on import → `PULSE_IMPORT_SET_OVERFLOW`. Inference picks the SMALLEST rung that fits the observed dictionary, so a 65-option column lands here and a 129-option one promotes to `set_u256`. Width is paid by every record: a 70-label column costs 16 bytes per row whether or not any row sets a high bit.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap; null is signalled there alone. Empty mask is distinct from null. In-band sentinels are explicitly disallowed for `set_*` types — no mask value is reserved.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block, shared across all records; `MaxDictEntries()` = `128`. Shard cohesion: union-merge under the 128-label cap; width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. The motivating source is an SPSS multiple-DICHOTOMY response set with more than 64 constituents, imported beside its constituent columns (`cohort-schema-design`, SPSS import).

## See

- Skill: `cohort-schema-design` (Field-type matrix, Nullability + per-record bitmap, Width overflow).
- Cross-link: `type-set-u64` (the rung below), `type-set-u256`, `op-agg-set-frequency`, `op-attr-set-has`.
