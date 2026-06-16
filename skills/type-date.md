---
name: type-date
kind: type
description: Day-granularity date stored as epoch days (signed since 1970-01-01).
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian. Not bit-packed. Stride contributes 4 bytes to `Schema.RecordByteSize`. On the wire it is a 32-bit value; semantically it is days offset from 1970-01-01.

## Range

Days since the Unix epoch (1970-01-01). Negative offsets address pre-epoch dates; positive offsets address dates after. The 32-bit width supports a range comfortably wider than any real-world need. Sub-day precision is NOT supported — store sub-day timestamps as `u64` microseconds.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. There is no in-band sentinel — `0` is 1970-01-01, not null. `IsNumericForAnalytics()` is `true`, so aggregators treat the ordinal directly with null-skip semantics; `IsNumeric()` is `false` (excluded from strict scalar numeric).

## Dictionary

Absent. Dates are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix), `grouper-design` (`GROUP_DATE`).
- Cross-link: `type-u64` for sub-day timestamps.
