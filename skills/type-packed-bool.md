---
name: type-packed-bool
kind: type
description: 1-bit boolean; bit-packed with adjacent packed_bool / u4 fields.
type: reference
applies_to: inspect, predict
---

## Bytes

`ByteSize() == 0` — bit-packed. Each value is 1 bit; eight `packed_bool` fields share one byte. Stride math handled by `Schema.RecordByteSize`; place packed runs together for optimal layout. `FieldType.IsBitPacked()` reports `true`.

## Range

`0` or `1` — false or true. The canonical boolean encoding. Aggregators treat it as analytics-numeric (mean = positive rate, sum = positive count), with `IsNumericForAnalytics()` reporting `true`; strict `IsNumeric()` is `false`.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap (independent from the value bit). `0` is `false`, never null. Null-skip semantics apply uniformly — mean over `n` non-null records.

## Dictionary

Absent. `packed_bool` carries no inline dictionary block. The default grouper is `GROUP_CATEGORY` (two buckets); the default aggregator is `AGG_FREQUENCY`.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Bit-packed runs, Smart defaults).
- Cross-link: `type-u4` for small ordinals using the same packed-byte cursor.
