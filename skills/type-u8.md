---
name: type-u8
kind: type
description: 1-byte unsigned integer; fits cardinals through 255.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 1 byte per record. Not bit-packed (`IsBitPacked()` is `false`). Aligned at the byte boundary recorded in `Schema.Field.ByteOffset`; bit position is always `0`.

## Range

`0..255` (8-bit unsigned). Use for small counts, age in years, version ordinals, small enum domains where you do NOT need dictionary lookup, or as the discriminator under a packed value pair.

## Null

Orthogonal. Set `Nullable: true` and the per-record null bitmap records null state. `0` is the integer zero, not null — there is no in-band sentinel. Counts as `IsNumeric()` and `IsNumericForAnalytics()`; aggregators and regressions consume directly.

## Dictionary

Absent. No inline dictionary block. Reach for `categorical_u8` (also 1 byte on the wire) when the column is string-labeled; the encoder stores the dictionary once in the schema and the record pays only the index byte.

## See

- Skill: `cohort-schema-design` (Field-type matrix).
- Cross-link: `type-categorical-u8` for labeled-enum alternative.
