---
name: type-u8
kind: type
description: 1-byte unsigned integer; fits cardinals through 255.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 1 byte per record at the field's `ByteOffset`. Not bit-packed; `BitPosition` is 0.

## Range

`0..255`. Inference picks `u8` for sampled integers in 16..255 (0..15 infers `u4`); a later value past 255 promotes the field (`PULSE_IMPORT_WIDTH_PROMOTED`). Fits small counts, age in years, small integer codes that need no dictionary.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. `0` is the integer zero, never null. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators and regressions read it directly and skip nulls.

## Dictionary

Absent. For a string-labelled field use `categorical_u8` (also 1 byte per record; the dictionary is stored once in the schema).

## See

- Skill: `cohort-schema-design` (Field-type matrix).
- Cross-link: `type-categorical-u8`, `type-u4`.
