---
name: type-u32
kind: type
description: 4-byte unsigned integer; the common wide-cardinal default.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian. Not bit-packed.

## Range

`0..4,294,967,295` (~4.29B). Inference picks `u32` for sampled integers in 65,536..2^32−1; a later value past the rung promotes the field to `u64` (`PULSE_IMPORT_WIDTH_PROMOTED`). The usual home for surrogate keys, counts and sequence numbers.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. `0` is the integer zero, never null. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators and regressions read it directly and skip nulls.

## Dictionary

Absent. For a wide labelled enum use `categorical_u32` (same 4-byte index plus an inline dictionary); `set_u32` is the multi-select sibling.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics).
- Cross-link: `type-categorical-u32`, `type-set-u32`.
