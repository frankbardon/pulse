```yaml
name: materialize-recode
description: Relabel, merge, null out and reorder a materialized categorical column's values with output.columns[].recode — the dictionary rewrite, its order with type and as, the drift guard against a new source cohort, and the SPSS rebuild. Topical design; child of derived-cohorts.
type: guide
kind: design
applies_to: materialize, predict
covers: [recode, map, order, PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL]
requires: [capability:materialize, capability:materialize_output]
```

# Recoding materialized categoricals

An `output.columns` entry's `recode: {map, order}` rewrites the dictionary of the `categorical_*` column it reads — a source field or an inherited-categorical derived column ([`derived-cohorts`](derived-cohorts.md)). It is a dictionary rewrite, never a per-row expression.

```json
{"from": "answer", "as": "answer_3", "nullable": true,
 "recode": {"map": {"Yes": "yes", "Y": "yes", "No": "no", "N/A": null},
            "order": ["no", "yes", "Maybe"]}}
```

## The map

| `map` entry | Effect |
|---|---|
| `"Y": "yes"` (a new label) | relabel: `Y`'s entry is renamed, its position kept |
| `"Y": "yes"` and `"Yes": "yes"` | merge: one `yes` entry, at the first merged label's position |
| `"N/A": null` | the cells are written null and the entry leaves the dictionary; the OUTPUT column must be nullable (`nullable: true` on a non-nullable source) |
| a label not in `map` | kept as itself — unused dictionary entries too |

The written dictionary is the source's labels in code order, each replaced, first occurrence kept. `order` replaces that order: it lists EVERY recoded label exactly once, and codes follow it. Omit `order` to keep the source order.

## Order of operations on one column

1. `recode` — reads the source labels.
2. `type` — casts the RECODED labels ([`materialize-casts`](materialize-casts.md)): merging 257 labels down to 256 lets a `categorical_u16` narrow to `categorical_u8`; recoding `Yes` / `No` to `"1"` / `"0"` then `type: "u8"` gives numbers. A numeric column cannot be recoded, even with a categorical `type`.
3. The nullability check (`nullable: false` refuses a null).

`as` only names the result. `on_cast_error` is accepted on a recoded column; a recode alone never fails a value.

## Refusals — all before anything is read

| Code / reason | Cause |
|---|---|
| `PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL` | a `map` key the source dictionary does not hold — `details.label`, `from`, `suggestion` |
| `PULSE_MATERIALIZE_OUTPUT_INVALID` `recode_not_categorical` | the column read is not `categorical_*` |
| `recode_null_not_nullable` | a `null` value on a non-nullable output column (`details.label`) |
| `recode_order_unknown` / `_missing` / `_duplicate` | `order` names a label the recode does not write, omits one it does, or names one twice |

## Predict first — the drift guard

A stored reshape request (a request template with the cohort as a variable) meets a NEW source cohort each run. A source whose dictionary no longer holds a recoded label (renamed upstream, a typo fixed) is refused by predict statically, with the run's code and details — so predict every new source before writing. A NEW label the map does not name is never refused: it passes through unchanged — check predict's `columns[].dictionary` when that matters.

## Gotchas

- The recoded column's SPSS sidecar variable is rebuilt from the recoded dictionary (an unlabelled string variable of the new labels): the source's value labels never land on new codes; an untouched copy of the column keeps them.
- Keys and values are exact strings: no trimming, no case folding. An SPSS import's categoricals hold the numeric codes as labels (`"1"`, `"2"`).
- The same `from` may appear twice with different recodes under different `as` names.

## See

- [`derived-cohorts`](derived-cohorts.md) — the output slot and the two-step recipe.
- [`materialize-casts`](materialize-casts.md) — `type`, which runs after the recode.
- [`request-templating`](request-templating.md) — storing a reshape request.
