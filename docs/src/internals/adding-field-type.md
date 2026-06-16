# Adding a Field Type

**Audience:** Pulse internals contributors adding a new `.pulse` field
type — a new on-wire encoding the schema block can describe, the
record codec can read / write, and the operator surface can accept.

Adding a field type touches more layers than any other recipe — the
binary format, the schema reader, the operator-accept tables, the
predict routing layer, and the cohort-schema skill. It is also one of
the highest-impact extensions: every operator that touches the new
type either accepts it via its `AcceptsTypes` list or is excluded
from it explicitly.

## 1. Declare the FieldType constant

Add the `FieldType` constant and its `ByteSize()` method. The byte
size is the wire footprint per record; bit-packed types
(`packed_bool`, `u4`) return `0` and signal sharing via
`FieldType.IsBitPacked()`.

## 2. Schema reader

Wire the type byte into the schema reader case in `encoding/`. The
unknown-type-byte branch in the reader is what surfaces
`ENCODING_INVALID` for an unknown type — when you add the new constant,
add the corresponding decode arm.

If the new type carries a dictionary block (categoricals, sets),
follow the inline-dictionary contract documented in CLAUDE.md's
"Byte-layout invariants" — the dictionary lives between the schema
block and the record data, keyed by the position of the bearing field.

## 3. Update the operator accept tables

Every operator that should accept the new field type needs its row in
`descriptor/capabilities_*.go` extended. The accept tables are shared
slices declared near the top of `capabilities_aggregators.go`
(`numericFieldTypes`, `numericFieldTypesAnalytics`, `allCohortFieldTypes`,
`setFieldTypes`, …) — extend the right slice rather than adding the
new type to each operator individually.

## 4. Predict-side routing

If the new type changes how an operator behaves (categorical-vs-numeric
routing, nullable-vs-non-nullable handling), update
`descriptor/predict.go`'s routing tables (`numericAggregations`,
`categoricalAggregations`, …).

## 5. Update the cohort-schema-design skill

Add the new type to the "All field types" table in
`skills/cohort-schema-design.md`. `TestSkillsCoverAllFieldTypes`
enforces presence by name.

## 6. Update CLAUDE.md

The CLAUDE.md "Byte-layout invariants" section enumerates the
canonical field-type count and the bit-packed vs dictionary-bearing
distinctions. Update it to reflect the new type.

## 7. Run the gates

```bash
go test ./skills/ -run TestSkillsCoverAllFieldTypes
go test ./encoding/...
go test ./descriptor/ -run 'TestManifest|TestPredict'
```

The Update Demand row for `.pulse` format changes (header / field
type) covers all of these in one PR; see [The Update
Demand](update-demand.md).
