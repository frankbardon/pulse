---
name: type-f32
kind: type
description: IEEE-754 single-precision float; ~7 significant digits.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian IEEE-754 binary32. Not bit-packed.

## Range

±3.4e38, ~7 significant digits; every value widens exactly to `float64` on read. Inference picks `f32` only when asked (`--float f32`, MCP `float`); a later value outside f32's range then promotes the field to `f64` (`PULSE_IMPORT_WIDTH_PROMOTED`). Fits sensor readings, ratios and normalised scores where storage matters; use `f64` for sums where rounding compounds.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. A stored NaN is a value, not a null; `0.0` is zero. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators skip nulls.

## Dictionary

Absent. Floats are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics).
- Cross-link: `type-f64`, `type-decimal128` (exact money).
