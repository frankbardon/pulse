---
name: label-display
description: Translate categorical IDs to display labels at output time without rewriting the cohort. Use LabelBinding entries on Request / SampleRequest / FacetRequest / ExportJob when end users should see human-readable names where the data carries codes (country codes, ICD codes, product SKUs). Labels are display-only — filters, formulas, and sort keys still see raw values.
type: guide
applies_to: process, compose, sample, facet, inspect, predict
---

# Categorical label display

Pulse stores categorical fields as integer dictionary indices that resolve to compact string values on read (`"US"`, `"CA"`, `"M01.2"`). When the resolved string is itself a code rather than a display label, the **label overlay** lets a request rewrite or augment the value at output time without touching the on-disk schema.

## When to use

Use a label binding when:
- The cohort stores an identifier (ISO code, SKU, ICD code, internal enum) and the end user wants the human-readable name.
- The mapping is **runtime-controlled** — sourced from an external system, evolves independently of the data, or differs per audience (English vs. localised, internal vs. external).
- You can't or don't want to re-import the cohort with a denormalised label column.

Skip the overlay when:
- The label is intrinsic and stable — just import it as a second categorical column.
- The translation is computed (`x * 1.07`) — use `ATTR_FORMULA` instead.
- The mapping is part of the analytic semantics (used by filters or sort) — denormalise during import.

## Two-step setup

### 1. Register a label table

Tables live on `pulse.Options.Extensions.LabelTables` (single source of truth) or load from disk via `PULSE_LABEL_TABLES_DIR`.

```go
p, err := pulse.New(pulse.Options{
    Extensions: pulse.Extensions{
        LabelTables: map[string]pulse.LabelTable{
            "country_names": {
                Description: "ISO 3166-1 alpha-2 → display name",
                Rows: map[string]string{
                    "US": "United States",
                    "CA": "Canada",
                    "MX": "Mexico",
                },
            },
        },
    },
})
```

Function-driven tables wrap an external store:

```go
LabelTables: map[string]pulse.LabelTable{
    "icd_descriptions": {
        Lookup: func(key string) (string, bool, error) {
            v, ok := icdService.Lookup(key)
            return v, ok, nil
        },
    },
},
```

`PULSE_LABEL_TABLES_DIR` auto-loads `*.json` files; the filename (without `.json`) is the table name. File format is either a flat `{"US": "United States"}` map or a wrapped `{"description": "...", "rows": {...}}` object. Programmatic and disk-loaded tables can't share a name (`pulse.New` rejects the collision).

### 2. Attach a binding to the request

Each binding pairs a categorical field with a registered table and a mode:

```go
req := &pulse.Request{
    Cohort: &pulse.Cohort{Filename: "orders.pulse"},
    Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
    Aggregations: []*types.Aggregation{
        {Type: types.AGG_SUM, Field: "amount", Label: "total"},
    },
    Labels: []*pulse.LabelBinding{
        {Field: "country", Table: "country_names", Mode: pulse.LabelModeReplace},
    },
}
```

JSON form (consumed by `pulse_process`, `pulse api process`, etc.):

```json
{
  "labels": [
    {"field": "country", "table": "country_names", "mode": "replace"}
  ]
}
```

## Modes

### Replace

The value is rewritten in place. Group keys, aggregation map keys, sample row cells, and exported cells all show the label.

```json
{"country": "United States", "total": 15240.0}
```

When two source values map to the same label (legacy + current ISO codes both → `"United States"`), the output disambiguates by appending the source value in parentheses:

```json
[{"country": "United States (US)",  "total": 12000},
 {"country": "United States (USA)", "total":  3240}]
```

A `PULSE_LABEL_COLLISION` warning names every involved source value. Rows-backed tables get a pre-pass so both colliding sources render symmetrically. Function-driven tables can only detect collisions online — the first source to claim a label renders cleanly, the second renders disambiguated. Switch to **augment** if you need lossless round-trip semantics.

### Augment

The raw value is preserved and a sibling `"<field>_label"` column is added to every output row.

```json
{"country": "US", "country_label": "United States", "total": 15240}
```

If the schema already contains a field named `<field>_label` (or an aggregation/attribute label collides), the request is rejected with `PULSE_LABEL_FIELD_COLLISION` at validation time. Rename the existing column or switch to replace mode.

## Surface coverage

| Surface | Replace effect | Augment effect |
|---|---|---|
| `Sample` (`pulse.SampleWithRequest`) | row value → label | row + `country_label` |
| `Facet` (`FacetSchema`) | `FacetValueCount.Value` → label | sibling `FacetField` under `country_label` |
| `Process` group keys | group key value → label | sibling group column in each output row |
| `Process` `AGG_FREQUENCY` / `AGG_MODE` | result key → label | (sibling label column emitted by group integration) |
| `Export` / `Convert` | column value → label | extra column inserted after source |

Filters, formula attributes, sort keys, and group keys still see **raw** values. Labels never gate which records pass through the pipeline.

## Missing values

When a source value isn't in the table the resolver falls back to the raw resolved categorical string and accumulates a per-field miss count. The result envelope carries a single `PULSE_LABEL_LOOKUP_MISS` warning per bound field, summarising the unresolved-row count and naming the absent source values.

```json
"warnings": [
  {
    "code": "PULSE_LABEL_LOOKUP_MISS",
    "message": "field \"country\": 4 row(s) with values not in label table \"country_names\"",
    "details": {"unresolved_values": ["XK", "TW"], "unresolved_count": 4}
  }
]
```

## Validation surface

`descriptor.ValidateLabels` runs on every label-bound request before any record bytes are read. Errors surface in the predict envelope:

| Code | Meaning |
|---|---|
| `PULSE_LABEL_FIELD_UNKNOWN` | Binding references a field not in the schema. |
| `PULSE_LABEL_FIELD_NOT_CATEGORICAL` | Bound field is not `categorical_u8` / `u16` / `u32`. |
| `PULSE_LABEL_TABLE_UNKNOWN` | Table not registered on the Service. |
| `PULSE_LABEL_FIELD_COLLISION` | Augment sibling `<field>_label` collides with an existing schema field or aggregation label. |
| `PULSE_LABEL_DUPLICATE_BINDING` | Two bindings target the same field. |
| `PULSE_LABEL_COLLISION` (warning) | Two source values resolve to the same label in replace mode. |
| `PULSE_LABEL_LOOKUP_MISS` (warning) | One or more rows have categorical values absent from the table. |

## What labels do *not* do

- **They don't filter.** A filter on `country = "United States"` won't match — write `country = "US"` against the raw value.
- **They don't sort.** Sort keys see raw values.
- **They don't persist.** The `.pulse` file is unchanged; reopening with a different `Options.Extensions.LabelTables` produces different output without re-encoding.
- **They don't work for non-categoricals.** Labels translate dictionary string values; numeric / date / decimal fields have no dictionary key to translate. To label a numeric, derive a categorical column at import time.

## Worked example

```bash
# Disk-loaded table.
mkdir labels
cat > labels/country_names.json <<EOF
{"US":"United States","CA":"Canada","MX":"Mexico"}
EOF
export PULSE_LABEL_TABLES_DIR=$PWD/labels

# Replace mode at the CLI.
pulse api sample --input orders.pulse --count 10 \
    --labels country=country_names

# Augment via JSON request.
cat > req.json <<EOF
{
  "request": {
    "cohort": {"filename": "orders.pulse"},
    "groups": [{"type": "GROUP_CATEGORY", "field": "country"}],
    "aggregations": [{"type": "AGG_SUM", "field": "amount", "label": "total"}],
    "labels": [
      {"field": "country", "table": "country_names", "mode": "augment"}
    ]
  }
}
EOF
pulse api process --request req.json --json
```

The augment-mode response carries both `country` and `country_label` columns plus any `PULSE_LABEL_LOOKUP_MISS` warnings on the envelope.
