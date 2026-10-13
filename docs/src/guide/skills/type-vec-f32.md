```yaml
name: type-vec-f32
kind: type
description: Fixed-dim vector of float32 elements (dim 2..1024) stored as one field, with its kind, scale range and labels in the schema.
type: reference
applies_to: inspect, predict
```

## Bytes

Type byte 20. Fixed-width: 4 × dim bytes per record — dim little-endian IEEE-754 binary32 elements, packed contiguously, element 0 first. Not bit-packed. The dim is NOT in the type byte: it rides the schema's VECTORS section (format `0x02` extension block, tag 2, REQUIRED), so any cohort with a vector is written `0x02` and a binary predating vector fields refuses it loudly. `Schema.FieldWidth` is the one width; `FieldType.ByteSize()` is 0. The uint64 value API refuses it with `ENCODING_TYPE_MISMATCH` — use `encoding.ReadVector` / `WriteVector` (dim explicit).

## Range

Dim 2..1024. Elements are ~7 significant digits; each element widens exactly to float64 on read. A finite value beyond ±3.4e38 is refused on write (it would silently become ±Inf). Optional metadata per field: kind (`measure`, `scale`, `composition`, `binary`), a scale range (min < max) and exactly dim element labels.

## Null

Orthogonal. A null vector is a zeroed full stride plus its null-bitmap bit — no in-band sentinel. A NaN element is a stored value, not a null. `IsNumeric()` and `IsNumericForAnalytics()` are both `false`: a vector is not a scalar.

## Dictionary

Absent. Element labels live in the VECTORS section, not a dictionary block.

## See

- Skill: [`cohort-schema-design`](cohort-schema-design.md).
- Cross-link: [`type-vec-f64`](type-vec-f64.md).
