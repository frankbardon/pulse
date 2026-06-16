---
name: label-display
description: Translate categorical IDs to display labels at output time without rewriting the cohort. LabelBinding entries on Request / SampleRequest / FacetRequest / ExportJob. Display-only — filters / formulas / sort keys still see raw values.
type: guide
kind: design
applies_to: process, compose, sample, facet, inspect, predict
covers: [LabelBinding, pulse_label_tables, pulse_label_resolve]
---

# Label display

Pulse stores categorical fields as dictionary indices resolving to compact strings on read (`"US"`, `"M01.2"`, internal SKUs). When the resolved string is itself a code rather than a display label, the **label overlay** rewrites or augments the value at output time without touching the on-disk schema.

## When to use

Use a binding when the cohort stores an identifier (ISO code, SKU, ICD code, enum) and the user wants a human-readable name; OR the mapping is runtime-controlled (external store, evolves independently, varies per audience). Skip when the label is intrinsic and stable (import a second categorical column), the translation is computed (use `ATTR_FORMULA`), or the mapping is analytic semantics used by filters/sort (denormalise during import).

## Two-step setup

### 1. Register a label table

Tables live on `pulse.Options.Extensions.LabelTables` or load from disk via `PULSE_LABEL_TABLES_DIR`.

```go
LabelTables: map[string]pulse.LabelTable{
    "country_names": {Description: "ISO → name",
        Rows: map[string]string{"US": "United States", "CA": "Canada"}},
    // Function-driven for external stores:
    "icd_descriptions": {Lookup: func(k string)(string,bool,error){...}},
}
```

`PULSE_LABEL_TABLES_DIR` auto-loads `*.json` (filename without `.json` = table name). Either flat `{"US":"United States"}` or wrapped `{"description":"...","rows":{...}}`. Programmatic + disk-loaded can't share a name (`pulse.New` rejects).

### 2. Attach a binding

Each `LabelBinding` pairs a categorical field with a table and mode:

```json
{"labels":[{"field":"country","table":"country_names","mode":"replace"}]}
```

## Modes

**Replace** — value rewritten in place. Group keys, aggregation keys, sample row cells, exported cells show the label. Two source values mapping to the same label disambiguate by appending the source in parens (`"United States (US)"` vs `"United States (USA)"`) and emit `PULSE_LABEL_COLLISION`. Rows-backed tables pre-pass for symmetric rendering; function-driven tables detect collisions online only. Use **augment** for lossless round-trip.

**Augment** — raw value preserved; sibling `"<field>_label"` column added. Existing `<field>_label` or agg/attr label collision ⇒ `PULSE_LABEL_FIELD_COLLISION` at validation.

## Surface coverage

| Surface | Replace | Augment |
|---|---|---|
| `Sample` (`SampleWithRequest`) | row value → label | row + `<field>_label` |
| `Facet` (`FacetSchema`) | `FacetValueCount.Value` → label | sibling `FacetField` |
| `Process` group keys | group key → label | sibling column in each output row |
| `Process` `AGG_FREQUENCY` / `AGG_MODE` | result key → label | sibling label column |
| `Export` / `Convert` | column value → label | extra column inserted after source |

**Display-only.** Filters, formula attributes, sort keys, and group keys still see raw values. Labels NEVER gate which records pass through the pipeline.

## Missing values

When a source value isn't in the table the resolver falls back to the raw resolved categorical string and accumulates a per-field miss count. One `PULSE_LABEL_LOOKUP_MISS` warning per bound field summarises unresolved-row count and absent source values.

## Validation surface

`descriptor.ValidateLabels` runs on every label-bound request before any record bytes are read:

| Code | Meaning |
|---|---|
| `PULSE_LABEL_FIELD_UNKNOWN` | Binding references a field not in the schema. |
| `PULSE_LABEL_FIELD_NOT_CATEGORICAL` | Bound field is not `categorical_u8/u16/u32`. |
| `PULSE_LABEL_TABLE_UNKNOWN` | Table not registered. |
| `PULSE_LABEL_FIELD_COLLISION` | Augment sibling collides with an existing field or agg label. |
| `PULSE_LABEL_DUPLICATE_BINDING` | Two bindings target the same field. |
| `PULSE_LABEL_COLLISION` (warning) | Two source values resolve to the same label. |
| `PULSE_LABEL_LOOKUP_MISS` (warning) | Rows have categorical values absent from the table. |

## MCP discovery — `pulse_label_tables` + `pulse_label_resolve`

LLM clients discover label tables via two MCP tools. `pulse_label_tables` lists registered tables, row counts, and enumerability (reverse-searchable). `pulse_label_resolve` reverse-resolves a user-supplied display name (typo-tolerant; case/punct normalised) to the raw key a filter or grouper needs. Args: `table`, `query`, optional `limit` (default 10). Returns `{key, value, score}` with score in `[0,1]`: `1.0` exact, `≥0.9` prefix/near-typo, lower for fuzzy. Use the top hit when score ≥ 0.9 and clearly ahead; otherwise surface candidates and ask.

## What labels do NOT do

- They don't filter. Filter `country = "United States"` won't match; write `country = "US"`.
- They don't sort. Sort keys see raw values.
- They don't persist. The `.pulse` file is unchanged; reopening with a different `LabelTables` produces different output without re-encoding.
- They don't work for non-categoricals. To label a numeric, derive a categorical column at import time.

## See

- `skills/extension-points.md` — registering `LabelTables` on `Options.Extensions`.
- `skills/aggregation-guide.md` — `AGG_FREQUENCY` / `AGG_MODE` label-key path.
- `skills/facet-design.md` — surfacing labels on `FacetField` values.
- `skills/mcp-integration.md` — `pulse_label_tables` / `pulse_label_resolve` schemas.
