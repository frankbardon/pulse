---
name: type-set-u256
kind: type
description: 32-byte multi-select bitmask over an inline dictionary of up to 256 labels — the widest set rung.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 32 bytes per record, little-endian **word** order — `words[0]` holds bits 0..63 and occupies the lowest 8 bytes. Not bit-packed (the value is consumed atomically as a bitmask). Stride contributes 32 bytes. Bit `i` corresponds to dictionary entry `i` — bit set means "label `i` selected". Too wide for the `uint64` scalar value API: `encoding.ReadFieldValue` / `WriteFieldValue` REFUSE it with `ENCODING_TYPE_MISMATCH` rather than silently truncate to the low 64 bits. `FieldType.IsWideSet()` names that split; wide masks travel as `SetMask`.

## Range

Up to `MaxSetEntries()` = `256` distinct labels — the widest set width Pulse supports, and a HARD ceiling: there is deliberately no `set_u512`, so a 257-option column has no set type and stays as its constituent columns. Overflow on import → `PULSE_IMPORT_SET_OVERFLOW`. **Empty mask is a valid value meaning "no labels selected" — it is NOT null.** Inference picks the smallest rung that fits, so this width is reached only above 128 labels; a 206-option column pays 32 bytes per row and wastes 26 — accepted knowingly over a variable-width set type.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap; null is signalled there alone. Empty mask is distinct from null. In-band sentinels are explicitly disallowed for `set_*` types.

## Dictionary

Present (`HasDictionary()` = `true`). Inline schema block, shared across all records; `MaxDictEntries()` = `256`. Shard cohesion: union-merge under the 256-label cap; width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`, terminal here — no wider rung exists to promote into. Motivating source: a large SPSS multiple-DICHOTOMY response set, imported beside its constituent columns (`cohort-schema-design`, SPSS import).

## See

- Skill: `cohort-schema-design` (Field-type matrix, Nullability + per-record bitmap, Width overflow).
- Cross-link: `type-set-u128` (the rung below), `op-agg-set-frequency`, `op-attr-set-has`.
