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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `fit` | string | (required) | Bound model name. |
| `terms` | list | — | Subset of terms, `(intercept)` opt-in. Absent: whole model. |
| `scale` | enum | `response` | GLM, ordinal, multinomial: `response`, `link`. |
| `bound` | enum | `point` | `point`, `lower`, `upper`: ci bound × column. Needs `terms` = one predictor. |
| `carryover` | bool | `true` | Adstocked-in-model fit: carry (needs `order`). |
| `order` | object | — | `{field, partition?}` of raw spend (`regression-order`). |
| `output` | enum | — | Ordinal / multinomial column (`regression-row-scores`). |
| `level` | string | — | Ordinal level. |
| `category` | string | — | Multinomial category. |

## Inputs

- model predictors — numeric fields or earlier labels, by name
- `label` — output name; default `ATTR_REG_PREDICT_<fit>`

## Output

One `f64` per record; null when a value read is null, NaN or ±Inf, and on EVERY row when the fit failed on its data (`PULSE_FIT_UNAVAILABLE`; predict cannot foresee it). Training rows equal the fit's fitted values bit for bit.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- The fit's predictor list is authoritative: build the same fields (`regression-row-scores`).
- A coefficient bound is not a prediction interval. A term with no bound scores null.
- Row-local (two-pass with `order`): streams, merges, materializes. Row weight ignored.
- A registered model scores whole-model (`no_scorer` without a scorer); save, load, ordinal, multinomial: `regression-row-scores`.<!-- feature: REG_MMM_BAYES --> `REG_MMM_BAYES` alone takes a whole-model `bound` (`mmm-bayes-posterior`).<!-- /feature -->

## See

- `pulse_examples_search tags=[compose]`
- Skills: `regression-row-scores`, `compose-fits`, `regression-modeling`, `op-attr-reg-fitted`, `expression-language` (`coef()`)
