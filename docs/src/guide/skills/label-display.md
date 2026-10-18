```yaml
name: label-display
description: Translate categorical IDs to display labels at output time without rewriting the cohort. LabelBinding entries on Request / SampleRequest / FacetRequest / ExportJob. Display-only — filters / formulas / sort keys still see raw values.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict
covers: [LabelBinding, pulse_label_tables, pulse_label_resolve]
requires: [capability:labels]
```

# Label display

Categorical fields resolve to compact strings on read (`"US"`, `"M01.2"`). When that string is a code, not a display label, a **label binding** rewrites or augments it at output time; the cohort is untouched.

## When to use

Use a binding when the cohort stores an identifier (ISO code, SKU, ICD code) and the user wants a readable name, or the mapping is runtime-controlled. Skip when the label is intrinsic (import a second categorical column), computed (a formula attribute — [`expression-language`](expression-language.md)), or semantics filters/sort need (denormalise at import).

## Two-step setup

### 1. Register a label table

Tables live on `pulse.Options.Extensions.LabelTables`, or load via `PULSE_LABEL_TABLES_DIR`.

```go
LabelTables: map[string]pulse.LabelTable{
    "country_names": {Description: "ISO → name",
        Rows: map[string]string{"US": "United States", "CA": "Canada"}},
    // Function-driven for external stores:
    "icd_descriptions": {Lookup: func(k string)(string,bool,error){...}},
}
```

`PULSE_LABEL_TABLES_DIR` auto-loads `*.json` (filename minus `.json` = table name). Flat `{"US":"United States"}` or wrapped `{"description":"...","rows":{...}}`. Programmatic + disk-loaded can't share a name (`pulse.New` rejects).

**Pulse's own sidecars are skipped; any other unparseable `*.json` is fatal.** `*.spss.json`, `*.meta.json` and `*.indexes.json` are excluded by suffix (a cohort directory is safe); any OTHER unparseable `*.json` hard-fails `pulse.New` naming the path — a typo never becomes a silently missing table. `PULSE_RANGE_TABLES_DIR` behaves identically.

### 2. Attach a binding

Each `LabelBinding` pairs a categorical field with a table and mode:

```json
{"labels":[{"field":"country","table":"country_names","mode":"replace"}]}
```

## Modes

**Replace** — value rewritten in place. Group keys, aggregation keys, sample row cells, exported cells show the label. Two source values mapping to the same label disambiguate by appending the source in parens (`"United States (US)"` vs `"United States (USA)"`) and emit `PULSE_LABEL_COLLISION`. Use **augment** for lossless round-trip.

**Augment** — raw value preserved; sibling `"<field>_label"` column added. A schema field, or any output column (aggregation, attribute, group key, window, feature — default names included) named `<field>_label` ⇒ `PULSE_LABEL_FIELD_COLLISION` at validation, predict included.

## Surface coverage

| Surface | Replace | Augment |
|---|---|---|
| `Sample` (`SampleWithRequest`) | row value → label | row + `<field>_label` |
| `Facet` (`FacetSchema`) | `FacetValueCount.Value` → label | sibling `FacetField` |
| `Process` group keys | group key → label, and every `group_key` echoing it | sibling column in each output row |
| Crosstab matrix axes | `row_keys` / `column_keys` → label; overlay keys follow, a pairwise PAIR key rebuilt from the labelled legs | not applied: `PULSE_LABEL_NOT_APPLIED` (no sibling on a key; use `replace` or shape `long`) |
| `ProcessChain` stages | every stage's `labels` + `Options.AutoLabels`, bar a `replace` default on a key an earlier stage replaced | every stage |
| `Export` / `Convert` | column value → label | extra column inserted after source |
| `.sav` export | value labels on the code (table key order; `u4`–`u32` too), cells untouched | same — [`spss-export`](spss-export.md) |

Only key TEXT moves: overlay coordinates address the same host cell; no figure changes. A Compose overlay over slots bound differently refuses `PULSE_OVERLAY_KEY_SET_DIVERGENT`. **A binding that reaches no surface warns `PULSE_LABEL_NOT_APPLIED`** (`details.reason` `field_not_in_output`: the field is no output column, axis or group key — bind a grouped field); `AutoLabels` defaults never warn.

**Display-only.** Filters, formulas, sort keys and group keys see raw values; labels NEVER gate which records pass.

## Missing values

A value absent from the table falls back to the raw string; one `PULSE_LABEL_LOOKUP_MISS` per bound field summarises the unresolved rows and values.

## Validation surface

Validated before any record is read; `pulse_predict` reports the same codes:

| Code | Meaning |
|---|---|
| `PULSE_LABEL_FIELD_UNKNOWN` | Binding references a field not in the schema. |
| `PULSE_LABEL_FIELD_NOT_CATEGORICAL` | Bound field is not `categorical_*`. |
| `PULSE_LABEL_TABLE_UNKNOWN` | Table not registered. |
| `PULSE_LABEL_FIELD_COLLISION` | Augment sibling collides with an existing field or agg label. |
| `PULSE_LABEL_DUPLICATE_BINDING` | Two bindings target the same field. |
| `PULSE_LABEL_COLLISION` (warning) | Two source values resolve to the same label. |
| `PULSE_LABEL_LOOKUP_MISS` (warning) | Rows have categorical values absent from the table. |
| `PULSE_LABEL_NOT_APPLIED` (warning, run time) | A caller binding reached no output surface, or is augment on a matrix axis. |

## MCP discovery — `pulse_label_tables` + `pulse_label_resolve`

Labels are output-only; the INPUT direction needs a lookup. `pulse_label_tables` lists tables, row counts and enumerability. `pulse_label_resolve` turns a display name (typo-tolerant) into the raw key a filter or grouper needs, ranked `{key, value, score}`. Use the top hit when it scores ≥ 0.9 and is clearly ahead; otherwise ask.

## What labels do NOT do

- They don't filter. Filter `country = "United States"` won't match; write `country = "US"`.
- They don't sort. Sort keys see raw values.
- They don't persist. Different `LabelTables` give different output, no re-encoding.
- No non-categoricals (derive a categorical at import), bar `.sav` value labels.

## See

- `docs/src/internals/extension-points.md` — registering `LabelTables` on `Options.Extensions`.
- `skills/facet-design.md` — surfacing labels on `FacetField` values.
- `skills/tool-label-tables.md` / `skills/tool-label-resolve.md` — `pulse_label_tables` / `pulse_label_resolve` schemas.
- `skills/spss-cohorts.md` — why an SPSS cohort stores codes, and where its labels live.
