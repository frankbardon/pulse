```yaml
name: type-vec-f64
kind: type
description: Fixed-dim vector of float64 elements (dim 2..1024) stored as one field, with its kind, scale range and labels in the schema.
type: reference
applies_to: inspect, predict
```

## Bytes

Type byte 21. Fixed-width: 8 × dim bytes per record — dim little-endian IEEE-754 binary64 elements, packed contiguously, element 0 first. Not bit-packed. The dim is NOT in the type byte: it rides the schema's VECTORS section (format `0x02` extension block, tag 2, REQUIRED), so any cohort with a vector is written `0x02` and a binary predating vector fields refuses it loudly. `Schema.FieldWidth` is the one width; `FieldType.ByteSize()` is 0. The uint64 value API refuses it with `ENCODING_TYPE_MISMATCH` — use `encoding.ReadVector` / `WriteVector` (dim explicit). Every decode path returns it: `Cohort.Reader` cell `[]float64` (nil when null), the cohort builder takes the same slice of exactly dim elements, and an extension reads `extend.Record.VectorValue` (`float64` elements; `NumericValue` is always false). Sample / lookup rows carry the `float64` element array.

## Range

Dim 2..1024. Elements are ~15 significant digits, stored and read verbatim. Optional metadata per field: kind (`measure`, `scale`, `composition`, `binary`), a scale range (min < max) and exactly dim element labels.

## Null

Orthogonal. A null vector is a zeroed full stride plus its null-bitmap bit — no in-band sentinel. A NaN element is a stored value, not a null. `IsNumeric()` and `IsNumericForAnalytics()` are both `false`: a vector is not a scalar. Presence-only slots count a null vector as null and a NaN-element vector as present. Every slot that reads ONE value — a scalar aggregation, grouper, attribute, feature, filter, test or regression input, a window field / order key, a sort key, a weight, a date / zone slot, a point-lookup key — refuses a vector field with `PULSE_VECTOR_FIELD_NOT_SCALAR`, predict and runtime alike (`details.suggest` names the vector operator replacing a bare aggregation / grouper slot); a join key is `PULSE_JOIN_TYPE_MISMATCH` (`reason: vector_key`). Name the field under `vector` instead.

## Dictionary

Absent. Element labels live in the VECTORS section, not a dictionary block. Inspect reports them under the field's `vector` block (`dim`, `kind`, `scale_min` / `scale_max`, `labels`), truncated like a dictionary unless `FullDict`.

## See

- Skill: [`cohort-schema-design`](cohort-schema-design.md).
- Cross-link: [`type-vec-f32`](type-vec-f32.md).
