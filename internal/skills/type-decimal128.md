---
name: type-decimal128
kind: type
description: 128-bit fixed-point decimal with per-field (precision, scale); exact base-10 arithmetic.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 16 bytes per record, a two's-complement little-endian 128-bit integer mantissa. Not bit-packed. Each field's `precision` and `scale` live in the schema; the stored mantissa is the value × 10^scale.

## Range

Precision 1..38 significant digits, scale 0..precision; the mantissa magnitude is at most 10^38 − 1 (anything larger overflows). A division result takes at least 4 decimal places. Use for money, regulated metrics and balances, where base-10 rounding error is unacceptable.

**Output form:** a decimal STRING at the field's scale (`"-123.4567"`) wherever a value reaches a caller: record rows<!-- feature: capability:sample -->, `pulse_sample`<!-- /feature --><!-- feature: capability:lookup -->, `pulse_lookup`<!-- /feature -->, decimal aggregates, export. Window `order_by` and the request `sort` still order by value.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap, the only null mechanism. An all-zero mantissa is decimal zero, never null. `IsDecimal()` and `IsNumeric()` are both `true`, but aggregators without a decimal path refuse it (`PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`); each op skill's Contract lists the admitted types.

## Dictionary

Absent. Decimals are never dictionary-encoded.

## See

- Skills: `financial-cohorts` (rounding, result scale, aggregations), `cohort-schema-design` (Field-type matrix), `cohort-null-states`.
