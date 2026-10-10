---
name: op-attr-reg-predict
description: Per-row score from a bound model (a compose `fits` entry) — no refit; whole model or a terms subset.
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

| Name | Type | Description |
|---|---|---|
| `fit` | string | Bound model name (required). |
| `terms` | []string | Score only these terms, `(intercept)` opt-in; fit order. Omit: whole model. |
| `scale` | enum | GLM only: `response` (default, inverse link) or `link`. |
| `bound` | enum | `point` (default), `lower`, `upper`: ci bound × column. Needs `terms` = one predictor. |

## Inputs

| Param | Accepted field types |
|---|---|
| model predictors | numeric slot columns (schema, features, earlier labels) |
| `label` | output name; default `ATTR_REG_PREDICT_<fit>` |

## Output

One `f64` per record; null when a column the score reads is null. Training rows equal the fit's own fitted values bit for bit.

<!-- generated: reading-the-output -->

## Gotchas

- The fit's predictor list is authoritative: build the same columns (same features) in the slot. An absent one-hot column is the reference level; scoring never infers a level.
- A coefficient bound is not a prediction interval. A term with no bound scores null.
- Row-local: streams, merges, materializes. Row weight ignored (the fit's own applied).
- Under selection a dropped feature scores 0.

## See

- `pulse_examples_search tags=[compose]`
- Skills: `compose-fits`, `regression-modeling`, `op-attr-reg-fitted`, `expression-language` (`coef()`)
