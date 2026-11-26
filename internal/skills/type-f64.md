---
name: type-f64
kind: type
description: IEEE-754 double-precision float; ~15 significant digits.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 8 bytes per record, little-endian IEEE-754 binary64. Not bit-packed.

## Range

±1.8e308, ~15 significant digits. Inference picks `f64` for a fractional source column and for an integer column holding a negative value (Pulse has no signed integer type); materialized derived fields are `f64` too. The analytics default for continuous values. Use `decimal128` when exact base-10 arithmetic matters (money, regulated metrics).

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. A stored NaN is a value, not a null; `0.0` is zero. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators skip nulls. A non-finite result is JSON `null` and decodes back to NaN, never 0.

## Dictionary

Absent. Floats are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics), `financial-cohorts`.
- Cross-link: `type-decimal128`, `type-f32`.
