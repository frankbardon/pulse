```yaml
name: label-display
description: Translate categorical IDs and integer codes to display labels at output time without rewriting the cohort. LabelBinding entries on Request / SampleRequest / FacetRequest / ExportJob / ConvertJob. Display-only — filters / formulas / sort keys still see raw values.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict
covers: [LabelBinding, pulse_label_tables, pulse_label_resolve]
requires: [capability:labels]
```

# Label display

A categorical's string (`"US"`) or an integer field's number is often a code, not a display label: a **label binding** rewrites or augments it at output time; the cohort is untouched.

## When to use

Use a binding when the cohort stores an identifier (ISO code, SKU, survey code) to make readable, or the mapping is runtime-controlled. Skip when the label is intrinsic (import a second column), computed (a formula attribute — [`expression-language`](expression-language.md)), or filters/sort need it (denormalise at import).

## Two-step setup

### 1. Register a label table

Tables live on `pulse.Options.Extensions.LabelTables`:

```go
LabelTables: map[string]pulse.LabelTable{
    "country_names": {Description: "ISO → name",
        Rows: map[string]string{"US": "United States", "CA": "Canada"}},
    // Function-driven (external stores):
    "icd_descriptions": {Lookup: func(k string)(string,bool,error){...}},
}
```

Or `PULSE_LABEL_TABLES_DIR` auto-loads `*.json` (stem = table name), flat `{"US":"United States"}` or wrapped `{"description":"...","rows":{...}}`. Programmatic and disk tables can't share a name.

Pulse's own `*.spss.json` / `*.meta.json` / `*.indexes.json` sidecars are skipped; **any other unparseable `*.json` fails `pulse.New`**. `PULSE_RANGE_TABLES_DIR` alike.

### 2. Attach a binding

A `LabelBinding` pairs a field with a table and mode:

```json norun fragment
{"labels":[{"field":"country","table":"country_names","mode":"replace"}]}
```

## Modes

**Replace** (default) — value rewritten in place. Two source values mapping to one label disambiguate (`"United States (US)"`) and emit `PULSE_LABEL_COLLISION`. Use **augment** for lossless round-trip.

**Augment** — raw value preserved; sibling `"<field>_label"` column added. A schema field or output column (default names included) named `<field>_label` ⇒ `PULSE_LABEL_FIELD_COLLISION`, predict included.

**Integer codes.** A `u4`–`u32` field binds too, keyed by its codes' decimal spellings (`"1"`, `"7"`; else `PULSE_LABEL_KEY_INVALID`). On a response it is augment-only (`replace` ⇒ `PULSE_LABEL_REPLACE_NUMERIC`): the number stays, `<field>_label` is added: group keys, sample cells, long crosstab rows, every stage / slot; a facet counts it per code (no KS overlay). A matrix axis lacks one: `PULSE_LABEL_NOT_APPLIED`. Floats, `u64`, dates: `PULSE_LABEL_FIELD_NOT_CATEGORICAL`.

## Surface coverage

| Surface | Replace | Augment |
|---|---|---|
| `Sample` (`SampleWithRequest`) | row value → label | row + `<field>_label` |
| `Facet` (`FacetSchema`) | `FacetValueCount.Value` → label | sibling `FacetField` |
| `Process` group keys | group key → label, and every `group_key` echoing it | sibling column in each output row |
| Crosstab matrix axes | `row_keys` / `column_keys` → label; overlay keys follow, a pairwise PAIR key rebuilt from the labelled legs | not applied: `PULSE_LABEL_NOT_APPLIED` (no sibling on a key; use `replace` or shape `long`) |
| `ProcessChain` stages | every stage's `labels` + `Options.AutoLabels`, bar a `replace` default on a key an earlier stage replaced | every stage |
| `Export` / `Convert` (a converted `.sav` holds label text) | column value → label (a `u4`–`u32` code too, as text) | extra column after source |
| `.sav` export | value labels on the code (table key order; `u4`–`u32` too), cells untouched | same — [`spss-export`](spss-export.md) |

Only key TEXT moves; no figure changes. A Compose overlay over slots bound differently refuses `PULSE_OVERLAY_KEY_SET_DIVERGENT`. **A binding that reaches no surface warns `PULSE_LABEL_NOT_APPLIED`** (`details.reason` `field_not_in_output`); `AutoLabels` defaults (categorical only) never warn.

**Display-only.** Filters, formulas, sort keys and group keys see raw values; labels NEVER gate which records pass.

## Missing values

A value absent from the table keeps its raw value (augment: sibling null); one `PULSE_LABEL_LOOKUP_MISS` per bound field summarises the misses. A null cell is not looked up.

## Validation surface

Validated before any record is read; predict (`PredictConvert` too) reports the same codes:

| Code | Meaning |
|---|---|
| `PULSE_LABEL_FIELD_UNKNOWN` | Binding references a field not in the schema. |
| `PULSE_LABEL_FIELD_NOT_CATEGORICAL` | Bound field is neither `categorical_*` nor `u4`–`u32`. |
| `PULSE_LABEL_KEY_INVALID` | A `u4`–`u32` table key is no decimal value the field holds. |
| `PULSE_LABEL_REPLACE_NUMERIC` | Response: `replace` on a `u4`–`u32` field (use augment). |
| `PULSE_LABEL_TABLE_UNKNOWN` | Table not registered. |
| `PULSE_LABEL_FIELD_COLLISION` | Augment sibling collides with a field or output column. |
| `PULSE_LABEL_DUPLICATE_BINDING` | Two bindings target the same field. |
| `PULSE_LABEL_COLLISION` (warning) | Two source values resolve to the same label. |
| `PULSE_LABEL_LOOKUP_MISS` (warning) | Rows have values absent from the table. |
| `PULSE_LABEL_NOT_APPLIED` (warning, run time) | A caller binding reached no output surface, or is augment on a matrix axis. |

## MCP discovery — `pulse_label_tables` + `pulse_label_resolve`

`pulse_label_tables` lists tables, row counts and enumerability; `pulse_label_resolve` turns a display name (typo-tolerant) into the raw key a filter needs, ranked `{key, value, score}` — take the top hit at ≥ 0.9 when clearly ahead, else ask.

## What labels do NOT do

- They don't filter. Filter `country = "United States"` won't match; write `country = "US"`.
- They don't persist: other `LabelTables`, other output; no re-encoding.
- No floats, `u64` or dates (derive a categorical at import).

## See

- `docs/src/internals/extension-points.md` — registering `LabelTables` on `Options.Extensions`.
- `skills/facet-design.md` — surfacing labels on `FacetField` values.
- `skills/tool-label-tables.md` / `skills/tool-label-resolve.md` — `pulse_label_tables` / `pulse_label_resolve` schemas.
- `skills/spss-cohorts.md` — why an SPSS cohort stores codes, and where its labels live.
