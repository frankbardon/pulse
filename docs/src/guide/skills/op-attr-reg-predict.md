```yaml
name: op-attr-reg-predict
description: Per-row score from a bound model (a compose `fits` entry or a loaded `models` file) — no refit; whole model or a terms subset.
kind: operator
category: ATTR
operator: ATTR_REG_PREDICT
type: reference
applies_to: compose, predict
examples_tags: [regression, ols, compose]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values.

Questions it answers:

- How close are the model's predictions on weeks it was not fitted to (a holdout)?
- How much of each week's sales does the TV term of the fitted model account for?

Use something else:

- `ATTR_REG_FITTED` when the model should be fitted to these same rows inside the slot.

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

One `f64` per record; null when a column the score reads is null, and on EVERY row when the fit failed on its data (its `fits` entry carries `error`): the slot then warns `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}` and the response's other slots compute normally (strict mode fails the batch with the fit's code first). Predict cannot foresee a failed fit. Training rows equal the fit's own fitted values bit for bit.

## Reading the output

- `value`: The target value the named fitted model predicts for the row from the row's own predictor values: intercept + sum(coefficient * predictor) at the fit's full precision, in the target's units — for a GLM on the response scale (the inverse link applied: a probability, a count) unless scale is link. With terms, only the listed terms are summed: that part of the prediction, a contribution in the target's units (link units for a GLM).
  - Caveat: A coefficient's confidence bound gives the range of that one term, not a prediction interval for a row.

## Contract

- Types: `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (each column the bound model reads); any other scalar type is `PULSE_MODEL_PREDICTOR_NOT_NUMERIC`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- The fit's predictor list is authoritative: build the same columns (same features) in the slot. An absent one-hot column is the reference level; scoring never infers a level.
- A coefficient bound is not a prediction interval. A term with no bound scores null.
- Row-local: streams, merges, materializes. Row weight ignored (the fit's own applied).
- Under selection a dropped feature scores 0.
- An embedder-registered regression's fit scores through its registration's scorer, whole model only (no `terms`, no `lower`/`upper` `bound`); with none it is `PULSE_MODEL_SCORE_INVALID` reason `no_scorer`. Its saved model loads only where that regression is registered and enabled, else `PULSE_MODEL_REGRESSION_UNREGISTERED`.
- Save, then load: `save: "models/m.pulse-model.json"` on the fit; a later request declares `models: [{name: "m", path: "models/m.pulse-model.json"}]` and scores `fit: "m"` — bit-equal to the in-compose fit, any cohort with the same predictor columns.

## See

- `pulse_examples_search tags=[compose]`
- Skills: [`compose-fits`](compose-fits.md), [`regression-modeling`](regression-modeling.md), [`op-attr-reg-fitted`](op-attr-reg-fitted.md), [`expression-language`](expression-language.md) (`coef()`)
