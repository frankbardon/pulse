```yaml
name: derived-cohorts
description: Materialise a request's derived columns (features, attributes, windows) into a new cohort so a later request can regress, group, filter or join on them — when to, the two-step recipe, what the output holds.
type: guide
kind: design
applies_to: materialize, process
covers: [Materialize, MaterializeResult, pulse_materialize, derived columns]
requires: [capability:materialize, capability:process]
```

# Derived cohorts

A derived column — a window, a feature output, an attribute — exists only inside the request that computes it. Regressions, groupers, filters, joins and tests read COHORT fields, so they cannot name one (`SERVICE_VALIDATION`: unknown field). Materialising writes the derived columns as ordinary fields of a NEW cohort; the next request reads them like any other field.

Surfaces: MCP `pulse_materialize` ([`tool-materialize`](tool-materialize.md)), CLI `pulse api materialize -r REQ.json -o OUT.pulse [--overwrite] [--strict] [--json]`, library `Pulse.Materialize(ctx, req, out, MaterializeOptions{Overwrite, Strict})`.

## When to materialise

- A model over a window: a lagged, smoothed or cumulative predictor.
- Grouping or filtering on a derived value: a log-spend bucket, a rank.
- One derived column feeding several later requests: compute once, read many times.

A single request that only DISPLAYS a derived value needs no materialise step: run it directly. A chain stage cannot carry windows or features ([`process-chain`](process-chain.md)), so a chain is no substitute.

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

Step 2 — point the next request at `out` and reference the column by its label: a regression with `"predictors": ["spend_lag1"]`, a grouper on it, a filter on it.

Label every output you will reference; without a label the column takes the operator's default output name (the `derived_columns` list in the result names each one).

## What the output holds

- **Rows:** one per filter-passing source record, in source order (an archive's shards in archive order) — or in `sort` order, keyed on source fields or derived columns with `pulse_process`'s comparator. `sort` moves rows, never values: each window already ran under its own `order_by`. Features see every record; attributes and windows the survivors — exactly as `pulse_process` computes them.
- **Columns:** the source fields verbatim (types, nullability, dictionaries, descriptions), then one nullable `f64` per derived output in pipeline order — feature outputs (every output of a multi-output feature), attributes, windows. Each gets a generated description naming operator, field, params, partition / order, the frame only when the operator reads it (not when its manifest entry says `ignores_frame`) and the source cohort; an operator flagged `reads_expression` (a formula) is described by its expression verbatim (`<TYPE> = a * 0.9 — derived from …`).
- Integer window outputs (row number, rank) are stored as `f64`; a non-numeric output (a lag over a categorical) is `PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC`.
- Always a single-file cohort. The source's SPSS metadata sidecar is carried (rewritten for the new columns, so value labels survive to a `.sav` export; a stale one warns with its own code and is dropped); no other sidecar is — rebuild an index on the new cohort if you need one.

## Refusals

All before anything is written; the write itself is atomic.

| Code | Cause | Fix |
|---|---|---|
| `PULSE_MATERIALIZE_SLOT_UNSUPPORTED` | a slot other than cohort, label, features, filterers, attributes, windows, sort, weight, time_zone, disable_components (`details.slot`) | move that slot to the step-2 request |
| `PULSE_MATERIALIZE_NO_DERIVED` | no feature, attribute or window | nothing to write: run the request directly |
| `PULSE_MATERIALIZE_OUTPUT_UNDECLARED` | an embedder-registered feature, whose outputs cannot be named before it runs | apply it in the step-2 request instead |
| `SERVICE_VALIDATION` | `out` exists | pick a new path, or `overwrite` (stale sidecars are listed, never rebuilt) |

Process's own pre-flight runs first, so a request `pulse_process` refuses is refused here with the same code.

## Gotchas

- **Windows need their frame.** A cumulative window without `frame` (`{"mode": "rows", "preceding": null, "following": 0}`) sums the whole partition on every row.
- **Filters shape the rows.** A window over the survivors differs from one over every record; filter in step 1 only when step 2 should see the same population.
- **Re-materialise after the source changes**: the output is a snapshot, not a view.
- **Do not round-trip through an export and `pulse_import`.** Re-importing a streamed result is lossy (types re-inferred, columns projected); materialise instead.
