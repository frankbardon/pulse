```yaml
name: type-vec-f64
kind: type
description: Fixed-dim vector of float64 elements (dim 2..1024) stored as one field, with its kind, scale range and labels in the schema.
type: reference
applies_to: inspect, predict
```

## Bytes

Type byte 21. Fixed-width: 8 × dim bytes per record — dim little-endian IEEE-754 binary64 elements, contiguous, element 0 first. Not bit-packed. The dim is NOT in the type byte: it rides the schema's VECTORS section (format `0x02`, tag 2, REQUIRED), so a binary predating vectors refuses the cohort loudly. `Schema.FieldWidth` is the one width; `ByteSize()` is 0. The uint64 value API refuses it (`ENCODING_TYPE_MISMATCH`); use `encoding.ReadVector` / `WriteVector`. Decoders return it as `[]float64` (`Cohort.Reader`, nil when null) or `[]float64` (`extend.Record.VectorValue`, sample / lookup rows). Import, naming, export and refusals: [`vector-fields`](vector-fields.md).

## Range

Dim 2..1024. Elements are ~15 significant digits; each stored and read verbatim. Optional per field: kind (`measure`, `scale`, `composition`, `binary`), a scale range (min < max), exactly dim element labels.

## Null

Orthogonal: a null vector is a zeroed stride plus its null-bitmap bit. A NaN element is a stored value, not a null. `IsNumeric()` is `false`: a slot reading one value refuses it with `PULSE_VECTOR_FIELD_NOT_SCALAR` — name it under `vector` instead.

## Dictionary

Absent. Element labels live in the VECTORS section; inspect reports them under the field's `vector` block (`dim`, `kind`, `scale_min` / `scale_max`, `labels`), truncated unless `FullDict`.

## See

- Skill: [`vector-fields`](vector-fields.md), [`cohort-schema-design`](cohort-schema-design.md).
- Cross-link: [`type-vec-f32`](type-vec-f32.md).
