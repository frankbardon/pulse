```yaml
name: type-u4
kind: type
description: 4-bit unsigned integer; bit-packed into one whole record byte of its own.
type: reference
applies_to: inspect, predict
```

## Bytes

Bit-packed (`IsBitPacked()` is `true`, `ByteSize()` is 0), yet each `u4` takes one whole record byte (`Schema.FieldWidth` = 1); fields never share a byte, so field order does not change the stride. Pulse writers store the value in the low nibble (`BitPosition` 0; a reader takes the high nibble when `BitPosition` > 0).

## Range

`0..15`. Inference picks `u4` for an integer field whose sampled values all lie in 0..15 (a 0/1-only field infers `packed_bool`); a later value past 15 promotes it up the integer ladder (`PULSE_IMPORT_WIDTH_PROMOTED`). Fits small ordinals: Likert 1..5, grades, tiers.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. `0` is the integer zero, never null. Analytics-numeric (`IsNumericForAnalytics()`), so aggregators skip nulls; strict `IsNumeric()` is `false`.

## Dictionary

Absent; values are plain unsigned integers. For labelled categories use `categorical_u8`, or bind a label table at read time.

## See

- Skill: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix, Bit-packed runs).
- Cross-link: [`type-packed-bool`](type-packed-bool.md), [`type-u8`](type-u8.md).
