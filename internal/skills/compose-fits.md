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
<!-- feature: ATTR_REG_PREDICT -->
- A slot scores a fit per row with `ATTR_REG_PREDICT` (`params: {fit: "<name>"}`, optional `terms` / `scale` / `bound`): no refit, full precision, null on a null predictor; see `op-attr-reg-predict`.
<!-- /feature -->
- A slot's formula or filter expression reads a fit's figure with `coef("<name>", "<term>"[, "point|lower|upper|se"])`; see `expression-language`.

## Response

`fits` on the compose response is an object keyed by fit name, each value the fit's full regression result (coefficients, standard errors, p-values, fit statistics; covariance when the spec asks for it). It is omitted without fits. It sits outside the compose-level `return` — always emitted, never rounded — because slots score from those exact figures. Slot responses are unchanged: a fit adds no row to any slot.

<!-- feature: capability:model_save -->
## Saving

`save: "models/mmm.pulse-model.json"` on an entry (or on any regression spec) writes the fitted model as a versioned model file under the data root once it has run: atomic, full precision, named after the fit. The path must end `.pulse-model.json` and stay under the root (`PULSE_MODEL_SAVE_INVALID`, checked before any fit runs); a failed write is `PULSE_MODEL_WRITE_FAILED`; replacing a file succeeds with `PULSE_MODEL_OVERWRITTEN` in the compose response's `warnings` (`details.fit`). Two saves naming one file anywhere in the batch are refused (`reason: duplicate`, `details.sites`); every save path — fits and slots — is checked before any fit or slot runs.
<!-- /feature -->

<!-- feature: capability:models -->
## Loading

`models: [{name: "mmm", path: "models/mmm.pulse-model.json"}]` on any request (slot, fit request, process, chain stage, facet) loads a saved model under `name` into the same namespace: references resolve it exactly like a fit (bit for bit), on any cohort holding its predictor columns (provenance is informational). Every file is read before anything runs — in a batch, before any fit — so a load sees the file as it was before the batch. Refusals: `PULSE_MODEL_REF_INVALID` (no name, bad path), `PULSE_MODEL_FILE_UNREADABLE`, `PULSE_MODEL_FILE_INVALID`, `PULSE_MODEL_VERSION_UNSUPPORTED`, and `PULSE_MODEL_NAME_COLLISION` for a name a fit or another entry binds (`details.sources` names both).
<!-- /feature -->

## Refusals

- `PULSE_FIT_INVALID` with `details.reason`: `name_missing`, `request_missing`, `regression_count` (with `count`), `grouped`. Checked before any fit or slot runs.
- A fit request's own refusal (an unknown field, a bad regression spec) keeps its code and gains `details.fit`, the 0-based entry index — as a slot's gains `details.request`. With several failing fits the lowest index is reported.
- Every refusal locates the fit with `details.fit`.

## Validate before executing

`pulse_predict` with `composed` (`PredictCompose`) runs the same structural rules, then checks each fit request like a slot plus its regression spec, located by `details.fit`; a valid batch lists `fits: [{name, type, target, predictors, cohort}]` — predictors as written, never fitted. Predict never reads a record.

## See

`compose-requests` (slots, labels, parallel)<!-- feature: capability:compose_sweep --> · `compose-sweeps` (the slots a sweep adds receive the fits too)<!-- /feature --> · `regression-modeling` (choosing the regression a fit runs) · `regression-inference` (reading its result).
