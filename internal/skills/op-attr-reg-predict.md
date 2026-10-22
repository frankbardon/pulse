---
name: op-attr-reg-predict
description: Per-row score from a bound model (a compose `fits` entry or a loaded `models` file) — no refit; whole model or a terms subset.
kind: operator
category: ATTR
operator: ATTR_REG_PREDICT
type: reference
applies_to: compose, predict
examples_tags: [regression, ols, compose]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `fit` | string | (required) | Bound model name. |
| `terms` | list | — | Score only these terms, `(intercept)` opt-in; fit order. Absent: whole model. |
| `scale` | enum | `response` | GLM only: `response` (inverse link) or `link`. |
| `bound` | enum | `point` | `point`, `lower`, `upper`: ci bound × column. Needs `terms` = one predictor. |

## Inputs

- model predictors — numeric slot columns (schema, features, earlier labels)
- `label` — output name; default `ATTR_REG_PREDICT_<fit>`

## Output

One `f64` per record; null when a column the score reads is null, and on EVERY row when the fit failed on its data (`PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; other slots compute normally; predict cannot foresee it). Training rows equal the fit's own fitted values bit for bit.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- The fit's predictor list is authoritative: build the same columns (same features) in the slot. An absent one-hot column is the reference level; scoring never infers a level. Under selection a dropped feature scores 0.
- A coefficient bound is not a prediction interval. A term with no bound scores null.
- Row-local: streams, merges, materializes. Row weight ignored (the fit's own applied).
- A registered model scores whole-model only, through its scorer (`no_scorer` without one); save-then-load recipe: `regression-row-scores`.

## See

- `pulse_examples_search tags=[compose]`
- Skills: `regression-row-scores`, `compose-fits`, `regression-modeling`, `op-attr-reg-fitted`, `expression-language` (`coef()`)
