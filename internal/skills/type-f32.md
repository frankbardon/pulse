---
name: type-f32
kind: type
description: IEEE-754 single-precision float; ~7 significant digits.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian IEEE-754 binary32. Not bit-packed. Stride contributes 4 bytes; reader uses `math.Float32frombits(binary.LittleEndian.Uint32(...))`.

## Range

±3.4e38 with ~7 decimal significant digits. Right-sized for measurement-grade signals: sensor readings, percentages, ratios, normalized scores. Reach for `f64` when you need wider dynamic range or cumulative sums where rounding error compounds.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. There is no in-band sentinel — NaN is the IEEE NaN value, not null; `0.0` is the float zero, not null. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators apply null-skip semantics.

## Dictionary

Absent. Floats are never dictionary-encoded — distinct cardinality is effectively unbounded.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics).
- Cross-link: `type-f64` for higher precision; `type-decimal128` for exact money.
