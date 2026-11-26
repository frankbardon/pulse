# Adding a Field Type

**Audience:** Pulse internals contributors adding a new `.pulse` field
type — a new on-wire encoding the schema block can describe, the
record codec can read / write, and the operator surface can accept.

Load `.claude/reference/byte-layout.md` first: most of its rules fail
silently. Adding a field type touches more layers than any other recipe — the
binary format, the schema reader, the operator-accept tables, the
predict routing layer, and the cohort-schema skill. It is also one of
the highest-impact extensions: every operator that touches the new
type either accepts it via its `AcceptsTypes` list or is excluded
from it explicitly.

## 1. Declare the FieldType constant

Add the `FieldType` constant in `encoding/field_type.go` and its
`ByteSize()` case. The byte
size is the wire footprint per record; bit-packed types
(`packed_bool`, `u4`) return `0` and signal sharing via
`FieldType.IsBitPacked()`.

Every stride, offset and skip sizes a field through
`encoding.Schema.FieldWidth(i)`, the one field-aware width — never
through `ByteSize()` directly. If the new type's width is not a
function of its type byte alone, teach `FieldWidth` (and only it).
`TestNoTypeOnlyWidthInStridePaths` fails on a type-only width call
outside its explained allow-list. `vec_f32` / `vec_f64` are the worked
example: the dim lives in the schema's VECTORS section
(`Schema.Vectors`), so a copy of the schema that drops it sizes the
field at 0 bytes — keep `Vectors` through every schema copy
(`Logical()`, `internal/encoding.CloneVectors`, join schemas).

## 2. Schema reader

Wire the type byte into the schema reader case in `encoding/` (the public schema nouns; the decode-plan and group remainder is `internal/encoding/`). The
unknown-type-byte branch in the reader is what surfaces
`ENCODING_INVALID` for an unknown type — when you add the new constant,
add the corresponding decode arm.

If the new type carries a dictionary block (categoricals, sets),
follow the dictionary contract in `.claude/reference/byte-layout.md`:
the dictionary lives between the schema block and the record data,
keyed by the position of the bearing field. A type whose schema needs
an extension section makes the file version `0x02`; the version is a
function of schema content, never a flag.

## 3. Update the operator accept tables

Every operator that should accept the new field type needs its row in
`internal/descriptor/capabilities_*.go` extended. The accept tables are shared
slices declared near the top of `capabilities_aggregators.go`
(`numericFieldTypesAnalytics`, `numericFieldTypesAnalyticsNoDecimal`,
`allCohortFieldTypes`, `nonSetFieldTypes`, …) and the per-operator
classes of `internal/fieldaccept` (read through `tableAcceptsTypes`) —
extend the right slice or class rather than adding the new type to each
operator individually.

## 4. Predict-side routing

If the new type changes how an operator behaves (categorical-vs-numeric
routing, nullable-vs-non-nullable handling), update
the accepted-types table (`internal/fieldaccept`) classes that judge it —
predict and the runtime both read that table.

## 5. Skills and the byte-layout contract

- Write the atomic skill `skills/type-<kebab>.md`
  (`.claude/reference/skill-pack.md`; copy a sibling such as
  `skills/type-u16.md`).
- Add the type to the field-types table in
  `skills/cohort-schema-design.md` (`TestSkillsCoverAllFieldTypes`).
- Record its width, null and dictionary rules in
  `.claude/reference/byte-layout.md`.

CLAUDE.md is not a companion and carries no field-type count.

## 6. Run the gates

```bash
go test -count=1 ./internal/skills/ -run 'TestOperatorHasAtomicSkill|TestAtomicSkillHasRequiredSections|TestSkillsCoverAllFieldTypes'
go test -count=1 ./encoding/... ./internal/encoding/... ./internal/fieldaccept/
go test -count=1 ./descriptor/ ./internal/descriptor/ -run 'TestManifest|TestPredict'
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs
```

The Update Demand row for `.pulse` format changes (header / field
type) covers all of these in one PR; see [The Update
Demand](update-demand.md).
