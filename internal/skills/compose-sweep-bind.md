---
name: compose-sweep-bind
description: fits[].from_sweep — bind the winning slot of a ranked Compose sweep as a named regression fit in the same batch, with its static signature, refusals and predict parity.
type: guide
kind: design
applies_to: compose, predict
covers: [FitSpec, FitFromSweep, ComposedRequest]
requires: [capability:compose_sweep_bind]
---

# Sweep-bound fits

A `fits` entry may bind the winner of the batch's ranked `sweep` instead of running its own request: the rank-1 sweep slot's regression, refitted on that slot's rows, under the entry's `name`. Explicit slots and `optimize` blocks then read it like any fit, so a search and its follow-up are one Compose call.

```jsonc
{
  "sweep": {
    "axes": [{"name": "cut", "values": [60, 90]}],
    "request": {
      "cohort": {"filename": "weekly.pulse"},
      "filterers": [{"type": "<range filter>", "field": "week", "values": ["0", "{{cut}}"]}],
      "regressions": [{"type": "<regression>", "name": "m", "target": "revenue", "predictors": ["tv"], "holdout": "week > 90"}]},
    "rank": {"by": "regressions.m.holdout.rmse", "order": "asc"}
  },
  "fits": [{"name": "best", "from_sweep": {"regression": "m"}}],
  "requests": [{"label": "scored", "cohort": {"filename": "weekly.pulse"}, "...": "reads best"}]
}
```

## The binding

- `from_sweep.regression` is the `name` of a regression in the sweep body. An entry sets `request` OR `from_sweep`, never both.
- The winner is `ranking[0]`: ties go to the earliest slot, and a slot that failed on its data is not ranked.
- The fit request is the winning slot's rows (cohort, joins, filters, windows, features, attributes less fitted vector batteries, weight, params) plus that one regression. Groups, crosstab, aggregations, tests<!-- feature: capability:matrices -->, matrices<!-- /feature -->, `return` and the regression's own `save` are dropped.
- The name joins the one fit namespace (`PULSE_MODEL_NAME_COLLISION`); a slot reads it with `coef("best", "<term>")`<!-- feature: ATTR_REG_PREDICT --> or `ATTR_REG_PREDICT` `params: {fit: "best"}`<!-- /feature -->.
<!-- feature: capability:model_save -->
- `save` on the entry writes the bound fit as a model file, like any regression fit.
<!-- /feature -->

## Static signature

The regression's `type`, `target` and `predictors` (and a model regression's term catalog) must be identical in every sweep slot: vary a window, a filter or a param, never the model's shape. Predict reads the signature off the first sweep slot, so coefficient terms, predictor columns and the names `optimize` reads validate with no data.

## Refusals

`PULSE_FIT_INVALID` with `details.fit` (entry index), `name` and `reason`, before anything runs, identical in predict and runtime:

- `from_sweep_with_request` — both `request` and `from_sweep`.
- `from_sweep_no_sweep` — the batch has no `sweep`.
- `from_sweep_no_rank` — the sweep has no `rank`, so no slot wins.
- `from_sweep_regression_unknown` — no body regression has that `name` (`details.regression`, `available`).
- `from_sweep_signature_varies` — type, target or predictors differ in a slot (`details.slot`, the 0-based sweep index).
- `from_sweep_cycle` — a sweep slot reads the fit (a literal `coef("best", …)` or a `params.fit`), but sweep slots run before it exists (`details.slot`).

## Predict

`pulse_predict` with `composed` (`PredictCompose`) lists the entry as `{name, type, target, predictors, cohort, from_sweep: {regression}}`. Which slot wins is unknown until the slots run.

## See

`compose-fits` (fits, names, failures) · `compose-sweeps` (the sweep and its `rank`)
