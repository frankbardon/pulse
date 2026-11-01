---
name: regression-row-scores
description: Per-row regression columns — the two-pass refit the fitted, residual and leverage attributes run, scoring a bound fit with params.fit instead, the scoring rule and its refusals, why the fit's predictor list is authoritative, a registered model's scorer, and the save-then-load recipe. Topical design; per-attribute params in the atomic op-attr-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ATTR_REG, fitted value, residual, leverage, scoring, params.fit, bound fit, save, load, no_scorer, slot_fields, PULSE_FIT_UNAVAILABLE]
---

# Per-row regression scores

Model coefficients come from a regression slot (`regression-modeling`); these attributes put a per-row figure from a model into a column.<!-- feature: capability:compose_fits --> Fits that several slots share: `compose-fits`.<!-- /feature -->

## Refit in the attribute prepass

<!-- feature: ATTR_REG_FITTED -->
- `ATTR_REG_FITTED` — the prediction ŷᵢ.
<!-- /feature -->
<!-- feature: ATTR_REG_RESIDUAL -->
- `ATTR_REG_RESIDUAL` — yᵢ − ŷᵢ.
<!-- /feature -->
<!-- feature: ATTR_REG_LEVERAGE -->
- `ATTR_REG_LEVERAGE` — the hat-matrix diagonal.
<!-- /feature -->

- `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` are top-level keys of the attribute entry, not `params`; `field` is unread. `decimal128` reads as its float64 echo, `packed_bool` as 0/1.
- Two-pass: each slot runs its own OLS fit in the prepass — no fit sharing between slots. Mergeable per shard; predict reports streamability.
- A weight follows OLS's kinds: a WLS refit. The residual stays the raw y − ŷ.
- A penalty reuses the same machinery: a mis-tuned `alpha` / `l1_ratio` shrinks coefficients toward zero, and penalized residuals are bias-shrunk — prefer an unpenalized fit for diagnostics.

## Scoring a bound fit

`params.fit` names a bound fit (a compose `fits` entry or a loaded `models` entry) instead of refitting — always on the predict attribute<!-- feature: ATTR_REG_PREDICT --> (`ATTR_REG_PREDICT`)<!-- /feature -->, optionally on the fitted and residual ones:

- Row-local: one pass, streams beside groupers, merges and materializes; it fits nothing. The score equals the bound model's bit for bit, and training rows equal the fit's own fitted values.
- The row weight is ignored; the fit's own weight applied when it ran.
- Fitted reads the fit's score (a GLM: its fitted mean); residual reads y − score, y the fit's target (a GLM: the response residual).
- Refusals, predict identically: an unknown name `PULSE_MODEL_UNKNOWN`; a refit field (`target`, `predictors`, `penalty`, …) beside it `slot_fields`; a registered model without a scorer `PULSE_MODEL_SCORE_INVALID` reason `no_scorer`.
- A fit that failed on its data (its `fits` entry carries `error`): every row null + `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; the response's other slots compute normally, and strict mode fails the batch with the fit's code first. Predict cannot foresee a failed fit.

<!-- feature: ATTR_REG_RESIDUAL -->
**Residual scale.** `ATTR_REG_RESIDUAL` over a bound fit takes `params.kind`, as R's `residuals.glm`:

- `response` (the default): y − μ.
- `pearson`: (y − μ)/√V(μ).
- `deviance`: sign(y − μ)·√d(y, μ).
- `working`: (y − μ)·g′(μ).

The slot carries no prior weight, so these are the unweighted forms. OLS and Bayesian-linear fits are Gaussian, so every kind equals y − μ. A registered or media-mix fit takes `response` only.
<!-- /feature -->

## The fit's predictor list is authoritative

Build the same columns (same features) in the scoring slot. An absent one-hot column is the reference level — scoring never infers a level. Under selection a dropped predictor scores 0. A predictor null on a row nulls that row.

On the predict attribute, `terms` scores a subset (`(intercept)` opt-in) and `bound` reads a coefficient's `lower` / `upper` for one term: a coefficient bound is NOT a prediction interval, and a term with no bound scores null.

## A registered model

An embedder-registered regression scores through its registration's scorer, whole model only (no `terms`, no `lower` / `upper` bound); with none it is `no_scorer`. Its saved model loads only where that regression is registered and enabled, else `PULSE_MODEL_REGRESSION_UNREGISTERED`.

<!-- feature: capability:model_save, capability:models -->
## Save, then load

`save: "models/m.pulse-model.json"` on the fit; a later request declares `models: [{name: "m", path: "models/m.pulse-model.json"}]` and scores `fit: "m"` — bit-equal to the in-compose fit, on any cohort with the same predictor columns.
<!-- /feature -->

## See

- `regression-modeling`, `compose-fits`, `regression-spec`, `attribute-composition`
