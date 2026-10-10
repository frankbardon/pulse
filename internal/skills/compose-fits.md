---
name: compose-fits
description: ComposedRequest.fits — named regression fits that run once before any Compose slot (sweep slots included) and bind into every slot by name, with their own rows, the response `fits` map, refusals and predict parity.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, FitSpec, ComposedResponse]
requires: [capability:compose_fits]
---

# Compose fits

A **fit** is a named regression that a Compose batch runs ONCE, before any slot, so every slot reads the same fitted model by name instead of refitting it or retyping its coefficients. Use it when several slots — a train and a holdout, a set of scenarios, a sweep — must all use one model.

```jsonc
{
  "fits": [
    {"name": "mmm",
     "request": {
       "cohort": {"filename": "weekly.pulse"},
       "filterers": [{"type": "<range filter>", "field": "week", "values": ["0", "90"]}],
       "features": [{"type": "<feature>", "field": "tv_spend", "label": "tv_sat"}],
       "regressions": [{"type": "<regression>", "target": "revenue", "predictors": ["tv_sat", "price"]}]}}
  ],
  "requests": [
    {"label": "train",   "cohort": {"filename": "weekly.pulse"}, "...": "..."},
    {"label": "holdout", "cohort": {"filename": "weekly.pulse"}, "...": "..."}
  ]
}
```

## The fit request

- `request` is an ordinary request holding exactly ONE regression and no `groups` or `crosstab` — one model over one row set. Fit a second model as a second entry.
- Its own cohort, filters, features and weight decide the rows the model is fit on, independently of every slot. Fitting weeks 0–90 while a slot filters weeks 91–103 is the train/holdout split; no split parameter exists.
- Its `return` is ignored: the whole result is always reported.
- Every fit runs exactly once per batch, whatever the slot count; under parallel compose the fits run concurrently among themselves, and serial and parallel answers are byte-identical.

## Names

- `name` is required and unique. Fits share ONE name namespace; a duplicate is `PULSE_MODEL_NAME_COLLISION`, and a reference to a name nothing binds is `PULSE_MODEL_UNKNOWN` (`details.available` lists the bound names).
- Every slot receives every fit — explicit slots and sweep-expanded slots alike — so a sweep axis can vary the slot while the model stays fixed.
- The model's predictor list is authoritative. A slot that scores against it supplies those columns itself (its own features); a one-hot column left out of `predictors` is the reference level, never inferred.

## Response

`fits` on the compose response is an object keyed by fit name, each value the fit's full regression result (coefficients, standard errors, p-values, fit statistics; covariance when the spec asks for it). It is omitted without fits. It sits outside the compose-level `return` — always emitted, never rounded — because slots score from those exact figures. Slot responses are unchanged: a fit adds no row to any slot.

## Refusals

- `PULSE_FIT_INVALID` with `details.reason`: `name_missing`, `request_missing`, `regression_count` (with `count`), `grouped`. Checked before any fit or slot runs.
- A fit request's own refusal (an unknown field, a bad regression spec) keeps its code and gains `details.fit`, the 0-based entry index — as a slot's gains `details.request`. With several failing fits the lowest index is reported.
- Every refusal locates the fit with `details.fit`.

## Validate before executing

`pulse_predict` with `composed` (`PredictCompose`) runs the same structural rules, then checks each fit request like a slot plus its regression spec, located by `details.fit`; a valid batch lists `fits: [{name, type, target, predictors, cohort}]` — predictors as written, never fitted. Predict never reads a record.

## See

`compose-requests` (slots, labels, parallel)<!-- feature: capability:compose_sweep --> · `compose-sweeps` (the slots a sweep adds receive the fits too)<!-- /feature --> · `regression-modeling` (choosing the regression a fit runs) · `regression-inference` (reading its result).
