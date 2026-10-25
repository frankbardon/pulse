---
name: compose-fits
description: ComposedRequest.fits — named regression or vector fits that run once before any Compose slot (sweep slots included) and bind into every slot by name, with their own rows, the response `fits` map, refusals and predict parity.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, FitSpec, ComposedResponse]
requires: [capability:compose_fits]
---

# Compose fits

A **fit** is a named model a Compose batch runs ONCE, before any slot, so every slot reads the same figures by name. Use it when slots (train / holdout, scenarios, a sweep) share one model.

```jsonc
{
  "fits": [
    {"name": "mmm",
     "request": {
       "cohort": {"filename": "weekly.pulse"},
       "filterers": [{"type": "<range filter>", "field": "week", "values": ["0", "90"]}],
       "regressions": [{"type": "<regression>", "target": "revenue", "predictors": ["tv_spend", "price"]}]}}
  ],
  "requests": [
    {"label": "train",   "cohort": {"filename": "weekly.pulse"}, "...": "..."},
    {"label": "holdout", "cohort": {"filename": "weekly.pulse"}, "...": "..."}
  ]
}
```

## The fit request

- `request` is an ordinary request, no `groups` / `crosstab`, holding exactly ONE regression (a **regression fit**) or ONE vector battery and no regression (a **vector fit**: one fitted vector attribute<!-- feature: capability:matrices --> or one `matrices` entry<!-- /feature -->, its listwise mean + co-moment)<!-- feature: MAT_SEM -->; one `MAT_SEM` entry is a **SEM fit** (`sem-fits`)<!-- /feature -->. Another model is another entry.
- Its own cohort, filters, features and weight decide the fit rows, independent of every slot (fit 0–90, score 91–103). Its `return` is ignored.
- Each fit runs once; serial and parallel are byte-identical.

A fit's regression may read same-request window labels (windows-over-records shape); a scoring slot's post-window<!-- feature: ATTR_REG_PREDICT --> `ATTR_REG_PREDICT`<!-- /feature --> runs after its windows; a `holdout` is scored at fit time (`regression-model-options`).

## Names

- `name` is required and unique. All kinds share ONE namespace; a duplicate is `PULSE_MODEL_NAME_COLLISION`, an unbound name `PULSE_MODEL_UNKNOWN` (`details.available`: bound names of that kind), a name of another kind `PULSE_MODEL_KIND_MISMATCH` (`details.fit`, `kind`, `expected`).
- Every slot, sweep-expanded too, receives every fit; a sweep axis varies the slot, not the model.
- A regression's predictor list is authoritative: a scoring slot supplies those columns; an absent one-hot column is the reference level.
<!-- feature: ATTR_REG_PREDICT -->
- A slot scores a fit per row with `ATTR_REG_PREDICT` (`params: {fit: "<name>"}`, optional `terms` / `scale` / `bound`): no refit, full precision, null on a null predictor; see `op-attr-reg-predict`.
<!-- /feature -->
- A slot's formula or filter expression reads a regression figure with `coef("<name>", "<term>"[, "point|lower|upper|se"])`; see `expression-language`.
- A vector fit is read by `params.fit` on a fitted vector attribute<!-- feature: capability:matrices --> or any matrix slot (`op-mat-correlation`)<!-- /feature -->: it emits from the bound state, reading no row of its own, matched BY LABEL under the fit's weight basis.

## Response

`fits` on the compose response is keyed by fit name: a regression fit's full regression result, a vector fit's `{"vector": {operator, labels, members, basis, n, <!-- feature: capability:weighting -->n_weight_invalid, sum_weights, <!-- /feature -->n_star, mean, covariance}}` (covariance M2*/(N* − 1); undefined cells `null`)<!-- feature: MAT_SEM -->, or a SEM fit's `{"sem": {…}}`<!-- /feature -->. Omitted without fits; outside the `return`, never rounded. A fit adds no row.

<!-- feature: capability:model_save -->
## Saving

`save` on an entry (or any regression spec) writes the fit under the data root once run, atomic and full precision, named after the fit: a regression as a model file (`models/mmm.pulse-model.json`), a vector fit as a population file (`pops/brand.pulse-population.json`), a SEM fit as `fits/brand.pulse-sem.json` (`sem-fits`). A wrong suffix or a path above the root is `PULSE_MODEL_SAVE_INVALID` (before any fit); a failed write `PULSE_MODEL_WRITE_FAILED`; replacing a file succeeds with `PULSE_MODEL_OVERWRITTEN` in compose `warnings` (`details.fit`). Two saves naming one file are refused (`reason: duplicate`).
<!-- /feature -->

<!-- feature: capability:models -->
## Loading

`models: [{name, path}]` on any request (slot, fit request, process, chain stage, facet, filter-to-file) loads a saved model (a `.pulse-sem.json`: a SEM fit) under `name` into the same namespace; `populations: [{name, path}]` (not on facet) loads a population file as a vector fit, read by `params.fit` and matched by label. A loaded name reads like the fit that saved it, on any cohort holding its columns. Files are read before anything runs. Refusals (`details.model` / `details.population`): `PULSE_MODEL_REF_INVALID`, `PULSE_MODEL_FILE_UNREADABLE`, `PULSE_MODEL_FILE_INVALID`, `PULSE_MODEL_VERSION_UNSUPPORTED`, `PULSE_MODEL_NAME_COLLISION` (`details.sources` names both).

Registered populations (`PULSE_POPULATIONS_DIR` or the embedder; manifest `extensions.populations`) are bound in every request already: reference one with `params.fit`. Reusing its name in an entry or fit is `PULSE_MODEL_NAME_COLLISION`.
<!-- /feature -->

## A failed fit

A fit its data refuses fails alone: its `fits` entry keeps identity + `error`, compose `warnings` gain `PULSE_SLOT_FAILED` (`details.fit`), nothing saves. Attributes reading it go null (`PULSE_FIT_UNAVAILABLE`), a matrix `params.fit` slot fails alone; a filter's `coef()`, a non-literal name, strict mode fail with its code.

## Refusals

- `PULSE_FIT_INVALID` with `details.reason`: `name_missing`, `request_missing`, `regression_count` (`count`), `kind_ambiguous` (regression AND battery), `vector_count`, `grouped`, `vector_source` (pairwise, a between-group distance, reverse-keyed reliability). Checked before any fit or slot runs.
- A fit request's own refusal keeps its code and gains `details.fit` (the 0-based entry index; the lowest when several fail).

## Validate before executing

`pulse_predict` with `composed` (`PredictCompose`) runs the same structural rules, then checks each fit request like a slot, located by `details.fit`; a valid batch lists `fits: [{name, type, target, predictors, cohort}]` (a vector or SEM fit: `{name, cohort, vector|sem: {operator, labels, members}}`) — never fitted, so a data failure is unforeseen; every `params.fit` name and kind is judged.

## See

`compose-requests` (slots, labels, parallel)<!-- feature: capability:compose_sweep --> · `compose-sweeps` (the slots a sweep adds receive the fits too)<!-- /feature --> · `regression-modeling` (choosing the regression a fit runs) · `regression-inference` (reading its result).
