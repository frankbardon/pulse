---
name: type-u4
kind: type
description: 4-bit unsigned integer; bit-packed into one whole record byte of its own.
type: reference
applies_to: inspect, predict
---

## Bytes

`ByteSize() == 0` — bit-packed, yet each `u4` takes one whole record byte (`Schema.FieldWidth` = 1); `BitPosition` picks the nibble (`> 0` high, `0` low). Fields never share a byte, so order does not change the stride. `FieldType.IsBitPacked()` reports `true`.

## Range

`0..15` (4-bit unsigned). Natural fit for small ordinals — Likert 1..5, letter grades, satisfaction tiers, or any closed cardinal scale that lives below 16 distinct values.

## Null

Orthogonal to type. Mark the field `Nullable: true` and the per-record null bitmap carries the actual null state. No inline sentinel — `0` is the integer zero, not null. `IsNumericForAnalytics()` is `true`, so null-skip aggregator semantics apply.

## Dictionary

Absent. `u4` carries no inline dictionary block; values are interpreted as plain unsigned integers. If you need labeled categories, prefer `categorical_u8` (with `≤256` dict entries).

## See

- Skill: `cohort-schema-design` (Field-type matrix, Bit-packed runs).
- Cross-link: `aggregation-design` for analytics-numeric coverage.
