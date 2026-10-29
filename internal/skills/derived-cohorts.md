---
name: derived-cohorts
description: Materialise a request's derived columns (features, attributes, windows) into a new cohort so a later request can regress, group, filter or join on them — when to, the two-step recipe, what the output holds.
type: guide
kind: design
applies_to: materialize, process
covers: [Materialize, MaterializeResult, pulse_materialize, derived columns]
requires: [capability:materialize, capability:process]
---

# Derived cohorts

A derived column — a window, a feature output, an attribute — exists only inside the request that computes it. Features, attributes and (windows-over-records) regressions read same-request labels; groupers, filters, joins and tests read COHORT fields, so they cannot name one (`SERVICE_VALIDATION`: unknown field). Materialising writes the derived columns as ordinary fields of a NEW cohort.

Surfaces: MCP `pulse_materialize` (`tool-materialize`), CLI `pulse api materialize -r REQ.json -o OUT.pulse [--overwrite] [--strict] [--json]`, library `Pulse.Materialize`.

Predict first (`predict: true` / `--predict` / `Pulse.PredictMaterialize`): header + schema only, nothing written, every refusal below with the run's code.

## When to materialise

- A model over a window: a lagged, smoothed or cumulative predictor.
- Grouping or filtering on a derived value: a log-spend bucket, a rank.
- One derived column feeding several requests: compute once.

A request that only DISPLAYS a derived value needs no materialise step. A chain stage cannot carry windows or features<!-- feature: capability:process_chain --> (`process-chain`)<!-- /feature -->, so a chain is no substitute.

## The two-step recipe

Step 1 — materialise (the request carries only derived columns and filters):

```jsonc
{
  "request": {
    "cohort": {"filename": "weekly.pulse"},
    "windows": [{
      "type": "<a WIN_* operator>", "field": "spend", "label": "spend_lag1",
      "partition_by": ["region"], "order_by": [{"field": "week"}]
    }]
  },
  "out": "weekly_derived.pulse"
}
```

Step 2 — point the next request at `out`, reference the column by its label: a regression with `"predictors": ["spend_lag1"]`, a grouper on it, a filter on it.

Label every output you will reference; unlabelled, a column takes the operator's default name (`derived_columns`).

## What the output holds

- **Rows:** one per filter-passing source record, in source order (an archive's shards in archive order) — or in `sort` order (source fields or derived columns, `pulse_process`'s comparator). `sort` moves rows, never values: each window ran under its own `order_by`. Features see every record; attributes and windows the survivors, as `pulse_process` computes them.
- **Columns:** the source fields verbatim (types, nullability, dictionaries, descriptions), then one nullable `f64` per derived output in pipeline order — feature outputs (every output of a multi-output feature), attributes, windows<!-- feature: ATTR_PREVIOUS_STATE -->; an `ATTR_PREVIOUS_STATE` output keeps its state field's `categorical_*` / `packed_bool` type and dictionary (nullable; the carried SPSS sidecar copies the state's value labels)<!-- /feature -->. Each gets a generated description (operator, field, params, partition / order, frame when read, source cohort; a formula by its expression verbatim).
- Integer window outputs (row number, rank) are `f64`; a non-numeric one (a lag over a categorical) is `PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC`.
- The source's SPSS sidecar is carried (rewritten for the new columns, so value labels survive a `.sav` export; a stale one warns with its own code and is dropped); no other sidecar is — rebuild an index on the new cohort.

<!-- feature: capability:materialize_output -->
## Reshaping a cohort

`output` lays out the written cohort, so a request with NO derived column is a pure reshape. Workflow:

1. Write `output`; 2. predict: the exact output `columns` and `risks`; 3. materialise; 4. to re-run the recipe over each new cohort, store it as a template with the cohort as `$var` (`request-templating`).

`columns` — `[{from, as, description, nullable, type, on_cast_error, recode, bucket}]` in list order — XOR `exclude`. `from` names a source field or a derived label; omitted keys inherit (a derived column stays a nullable `f64`). Projection runs LAST: filters, sort, weight and derived slots may read dropped columns. `nullable: false` over a null fails the run (`PULSE_MATERIALIZE_OUTPUT_NULL`, `details.row`); malformed blocks are `_OUTPUT_INVALID` / `_UNKNOWN_COLUMN` / `_DUPLICATE`; any other operation refuses the slot (`PULSE_MATERIALIZE_OUTPUT_ONLY`). The SPSS sidecar follows the layout; renames keep labels.

| To... | Key | Skill |
|---|---|---|
| retype a column, exact or failing | `type`, `on_cast_error` | `materialize-casts` |
| relabel, merge, null out, reorder categories | `recode` | `materialize-recode` |
| range numbers or dates into labelled bands | `bucket` | `materialize-recode` |
| shard the file, store constants once, declare parent groups | `shards`, `elide_constants`, `groups` | `materialize-container` |

An absent `output` writes the source columns verbatim plus the derived ones, byte-identically.
<!-- /feature -->

## Refusals

All before anything is written; the write is atomic.

| Code | Cause | Fix |
|---|---|---|
| `PULSE_MATERIALIZE_SLOT_UNSUPPORTED` | a slot outside cohort, label, features, filterers, attributes, windows, sort, weight, time_zone, output, disable_components (`details.slot`) | move that slot to the step-2 request |
| `PULSE_MATERIALIZE_NO_DERIVED` | no feature, attribute or window | nothing to write: run the request directly |
| `PULSE_MATERIALIZE_OUTPUT_UNDECLARED` | an embedder-registered feature, whose outputs cannot be named before it runs | apply it in the step-2 request instead |
| `SERVICE_VALIDATION` | `out` exists | pick a new path, or `overwrite` (stale sidecars are listed, never rebuilt) |

A request `pulse_process` refuses is refused here with the same code.

## Gotchas

- **Windows need their frame.** A cumulative window without `frame` (`{"mode": "rows", "preceding": null, "following": 0}`) sums the whole partition on every row.
- **Filters shape the rows.** A window over the survivors differs from one over every record; filter in step 1 only when step 2 should see that population.
- **Re-materialise after the source changes**: the output is a snapshot.
<!-- feature: capability:import -->
- **Do not round-trip through an export and `pulse_import`.** Re-importing a streamed result is lossy (types re-inferred, columns projected); materialise instead.
<!-- /feature -->
