---
name: type-f64
kind: type
description: IEEE-754 double-precision float; ~15 significant digits.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 8 bytes per record, little-endian IEEE-754 binary64. Not bit-packed. Stride contributes 8 bytes; reader uses `math.Float64frombits(binary.LittleEndian.Uint64(...))`.

## Range

±1.8e308 with ~15 decimal significant digits. The analytics default for any continuous numeric — scores, probabilities, summary statistics, model outputs. Sufficient for most scientific computing; reach for `decimal128` only when exact base-10 semantics matter (money, regulated metrics).

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. NaN is the IEEE NaN value, not null; `0.0` is the float zero, not null. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators apply null-skip semantics.

## Dictionary

Absent. Floats are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics).
- Cross-link: `type-decimal128` for exact base-10 arithmetic; `financial-cohorts`.
