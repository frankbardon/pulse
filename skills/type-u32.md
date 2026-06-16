---
name: type-u32
kind: type
description: 4-byte unsigned integer; the common wide-cardinal default.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian. Not bit-packed. Stride contributes 4 bytes to `Schema.RecordByteSize`; reader uses `binary.LittleEndian.Uint32`.

## Range

`0..4,294,967,295` (~4.29B). The default "I do not know the upper bound but it is not astronomical" unsigned integer. Surrogate keys, row counts, event sequence numbers, large-domain enums with no useful labels.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. `0` is the integer zero, never a sentinel. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators and regressions consume the values directly with null-skip semantics.

## Dictionary

Absent. For wide-cardinality labeled enums prefer `categorical_u32` — same 4-byte index width plus an inline dictionary block whose capacity is the full `u32` range. `set_u32` is the multi-select sibling.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics).
- Cross-link: `type-categorical-u32`, `type-set-u32`.
