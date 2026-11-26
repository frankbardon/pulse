---
name: type-vec-f64
kind: type
description: Fixed-dim vector of float64 elements (dim 2..1024) stored as one field, with its kind, scale range and labels in the schema.
type: reference
applies_to: inspect, predict
---

## Bytes

Type byte 21. Fixed-width: 8 × dim bytes per record — dim little-endian IEEE-754 binary64 elements, contiguous, element 0 first. Not bit-packed. The dim is not in the type byte: it lives in the schema's VECTORS section (format `0x02`, a required section, so a binary without vector support refuses the cohort). Size the field with `Schema.FieldWidth`; `ByteSize()` is 0. The uint64 value API refuses it (`ENCODING_TYPE_MISMATCH`); use `encoding.ReadVector` / `WriteVector`. Decoded as `[]float64` (`Cohort.Reader`, nil when null; `extend.Record.VectorValue`; sample / lookup rows). Import, naming, export and refusals: `vector-fields`.

## Range

Dim 2..1024. Elements carry ~15 significant digits, stored and read verbatim. Optional per field: kind (`measure`, `scale`, `composition`, `binary`), a scale range (min < max), exactly dim element labels.

## Null

Orthogonal: a null vector is a zeroed stride plus its null-bitmap bit. A NaN element is a stored value, not a null. A vector is not a scalar (`IsNumeric()` is `false`): a slot reading one value refuses it with `PULSE_VECTOR_FIELD_NOT_SCALAR` (`details.suggest` names the vector operator, when one replaces the slot); presence-only slots (a count of present or null rows, a null filter) accept it.

## Dictionary

Absent. Element labels live in the VECTORS section; `pulse_inspect` reports them under the field's `vector` block (`dim`, `kind`, `scale_min` / `scale_max`, `labels`), truncated unless `pulse cohort inspect --full-dict`.

## See

- Skills: `vector-fields`, `cohort-schema-design`.
- Cross-link: `type-vec-f32`.
