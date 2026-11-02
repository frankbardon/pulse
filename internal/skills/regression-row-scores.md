---
name: regression-row-scores
description: Per-row regression columns — the two-pass refit the fitted, residual, leverage and influence attributes run (studentized residuals, Cook's distance, DFFITS), scoring a bound fit with params.fit instead (its leverage and influence from the fit's vcov and dispersion), the scoring rule and its refusals, why the fit's predictor list is authoritative, a registered model's scorer, and the save-then-load recipe. Topical design; per-attribute params in the atomic op-attr-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ATTR_REG, fitted value, residual, leverage, influence, Cook's distance, DFFITS, studentized residual, GLM leverage, dispersion, scoring, params.fit, bound fit, save, load, no_scorer, slot_fields, PULSE_FIT_UNAVAILABLE]
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
<!-- feature: ATTR_REG_INFLUENCE -->
- `ATTR_REG_INFLUENCE` — Cook's distance (`params.measure` `cooks`) or DFFITS (`dffits`).
<!-- /feature -->

- `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` are top-level keys, not `params`; `field` is unread. `decimal128` reads as its float64 echo, `packed_bool` as 0/1.
- Two-pass: each slot runs its own OLS fit in the prepass, no sharing between slots.
- A weight follows OLS's kinds (a WLS refit); the residual stays the raw y − ŷ.
- Penalized residuals are bias-shrunk — diagnose on an unpenalized fit.

<!-- feature: ATTR_REG_RESIDUAL -->
**Studentized residuals.** On the refit `params.kind` `pearson` / `deviance` / `working` equal y − ŷ (OLS is Gaussian, identity). `standardized` (R `rstandard`) is e / (s·√(1 − h)) and `studentized` (R `rstudent`) r·√((df − 1)/(df − r²)), with h the leverage, df = n − p − 1 and s² = RSS/df.
<!-- /feature -->

**Design-reading figures** (leverage, influence, the two studentized kinds) read the hat diagonal and σ:

- Unpenalized only (`penalty` is `PROCESSING_CONFIG`).
- A frequency weight is w identical observations: influence and studentized figures are ONE copy's (h/w, df = Σw − p − 1). Leverage stays the record's summed h.
- A probability weight refuses the per-observation figures (`PULSE_WEIGHT_UNSUPPORTED`); leverage takes either kind.
- Null outside the fit and where undefined (df ≤ 1, h ≥ 1).

## Scoring a bound fit

`params.fit` names a bound fit (a compose `fits` entry or a loaded `models` entry) instead of refitting — always on the predict attribute<!-- feature: ATTR_REG_PREDICT --> (`ATTR_REG_PREDICT`)<!-- /feature -->, optionally on the other four:

- Row-local: one pass; it fits nothing. The score equals the bound model's bit for bit.
- The row weight is ignored; the fit's own weight applied when it ran.
- Fitted reads the fit's score (a GLM: its fitted mean); residual reads y − score, y the fit's target.
- Refusals, predict identically: an unknown name `PULSE_MODEL_UNKNOWN`; a refit field beside it `slot_fields`; a registered model without a scorer `no_scorer`.
- A fit that failed on its data: every row null + `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; strict mode fails the batch with the fit's code.

<!-- feature: ATTR_REG_RESIDUAL -->
**Residual scale.** `ATTR_REG_RESIDUAL` over a bound fit takes `params.kind`, as R's `residuals.glm`:

- `response` (the default): y − μ.
- `pearson`: (y − μ)/√V(μ).
- `deviance`: sign(y − μ)·√d(y, μ).
- `working`: (y − μ)·g′(μ).

Unweighted forms (the slot carries no prior weight); on a Gaussian fit every kind equals y − μ. A registered or media-mix fit takes `response` only.
<!-- /feature -->

**The design over a bound fit** — leverage, influence and the two studentized kinds, R 4.6.1's `hatvalues` / `rstandard` / `rstudent` / `cooks.distance` / `dffits` on the fit's lm or glm. x̃ = [1, x], μ the fitted mean, w = (dμ/dη)²/V(μ) (1 least-squares), rP the Pearson residual, φ the dispersion (1 binomial / poisson, else the fit's `dispersion`), p′ coefficients, df = n_obs − p′:

- h = w·x̃ᵀ(vcov/φ)x̃; standardized rP/(√φ·√(1 − h)) (`residual_type: deviance`: the deviance residual in place of rP, R `rstandard(type = "deviance")`; equal on a Gaussian fit, refused beside any other kind); cooks (rP/(1 − h))²·h/(φ·p′).
- studentized rP/(σ₍ᵢ₎·√(1 − h)), σ₍ᵢ₎² = (φ·df − rP²/(1 − h))/(df − 1) where φ is estimated; binomial / poisson: the standardized.
- dffits sign(g′(μ))·rP·√h/(σ·(1 − h)), σ = σ₍ᵢ₎ or √φ — the change on the link scale, so under the gamma inverse link it is NEGATIVE where rP is positive.
- The fit needs `vcov: true`, the classical covariance and no penalty, selection, resample, bounds or weight, else `PULSE_MODEL_SCORE_INVALID` `params_invalid` (`details.requires`); a Bayesian-linear or registered fit is `type_unsupported`. Weighted fits are not supported yet: refit with `weight: null`. A model file without `dispersion` (an earlier build's) needs a re-save.

## The fit's predictor list is authoritative

Build the same columns in the scoring slot. An absent one-hot column is the reference level. Under selection a dropped predictor scores 0. A null predictor nulls the row.

On the predict attribute, `terms` scores a subset (`(intercept)` opt-in) and `bound` reads a coefficient's `lower` / `upper` for one term: a coefficient bound is NOT a prediction interval, and a term with no bound scores null.

## A registered model

An embedder-registered regression scores through its registration's scorer, whole model only (no `terms`, no `lower` / `upper` bound); with none it is `no_scorer`. Its saved model loads only where that regression is registered and enabled, else `PULSE_MODEL_REGRESSION_UNREGISTERED`.

<!-- feature: capability:model_save, capability:models -->
## Save, then load

`save: "models/m.pulse-model.json"` on the fit; a later request declares `models: [{name: "m", path: "models/m.pulse-model.json"}]` and scores `fit: "m"` — bit-equal to the in-compose fit, on any cohort with the same predictor columns.
<!-- /feature -->

## See

- `regression-modeling`, `compose-fits`, `regression-spec`, `attribute-composition`
