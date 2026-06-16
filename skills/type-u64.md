---
name: type-u64
kind: type
description: 8-byte unsigned integer; for sub-day timestamps and very wide cardinals.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 8 bytes per record, little-endian. Not bit-packed. Stride contributes 8 bytes to `Schema.RecordByteSize`; reader uses `binary.LittleEndian.Uint64`.

## Range

`0..2^64 − 1`. The canonical home for sub-day timestamps stored as microseconds (or nanoseconds) since epoch — Pulse has no native sub-day temporal type, so the convention is `u64` micros. Also fits very wide surrogate keys and hash-derived identifiers.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. `0` is the integer zero, never a sentinel. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; null-skip semantics apply.

## Dictionary

Absent. There is no `categorical_u64` (the `u32` index already exceeds practical dictionary sizes) — labeled enums top out at `categorical_u32`.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics — sub-day timestamps as `u64` micros).
- Cross-link: `type-date` for day-granularity dates.
