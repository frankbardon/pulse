---
name: type-decimal128
kind: type
description: 128-bit fixed-point decimal with per-field (precision, scale); exact base-10 arithmetic.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 16 bytes per record, two-complement big-integer mantissa. Not bit-packed. Stride contributes 16 bytes to `Schema.RecordByteSize`. Per-field `(Precision, Scale)` lives in the schema; the encoded mantissa is the value × 10^Scale.

## Range

Up to 38 significant decimal digits (`MaxDecimalPrecision = 38`). Precision is the digit count; scale is the count after the decimal point and is bounded by `MinDecimalScale = 4` for division results. The first integer that overflows is 10^38 (`decimal128MaxAbs`). Use for money, regulated metrics, accounting balances — anywhere base-10 rounding error is unacceptable.

## Null

Orthogonal — and uniquely strict. `Nullable: true` participates in the per-record null bitmap, and the bitmap is the **only** null mechanism. All-zero mantissa bits is decimal zero, not null. `IsDecimal()` and `IsNumeric()` are both `true`.

## Dictionary

Absent. Decimals are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Nullability + per-record bitmap), `financial-cohorts`.
