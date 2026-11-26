---
name: op-reg-mod-selection
description: Spec-level subset-selection modifier (forward / backward / stepwise) that composes with an OLS, GLM, ordinal or multinomial fit; drives a greedy search over predictors against an information criterion. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_SELECTION
type: reference
applies_to: process, compose, predict
examples_tags: [regression, selection, stepwise, buffered-pipeline]
---

Regression operators emit coefficient + diagnostics; no `Response.Components`. This modifier shrinks the active predictor set in `regressions[i].selected_features` and drops non-selected entries from `coefficients` / `std_errors`.

<!-- generated: use-when -->

## Params

Top-level `selection` on the regression spec plus its required `criterion` companion. A **wrapper, not a fit**: the engine refits the host model against candidate subsets and keeps the lowest-criterion winner.

| Name | Type | Default | Description |
|---|---|---|---|
| `selection` | enum | — | `forward` (intercept-only, add best each step), `backward` (full, drop best each step), `stepwise` (bidirectional until no move improves); absent / `""` = off. |
| `criterion` | enum | — | `aic` or `bic`; required when `selection` is set (`SERVICE_VALIDATION`). |

## Inputs

Inherits `target` / `predictors` from the host spec. No additional field inputs.

| Host | Accepted? |
|---|---|
<!-- feature: REG_OLS -->
| `REG_OLS` | yes; with a `penalty`, predict warns `PROCESSING_REGRESSION_REGULARIZED_SELECTION` |
<!-- /feature -->
<!-- feature: REG_GLM -->
| `REG_GLM` | yes; with a `penalty`, predict warns as above |
<!-- /feature -->
<!-- feature: REG_BAYES_LINEAR -->
| `REG_BAYES_LINEAR` | refused, `PROCESSING_CONFIG` (predict too) |
<!-- /feature -->
<!-- feature: REG_ORDINAL -->
| `REG_ORDINAL` | yes — candidates keep the first fit's levels; k counts thresholds (`stepAIC` on `polr`) |
<!-- /feature -->
<!-- feature: REG_MULTINOMIAL -->
| `REG_MULTINOMIAL` | yes — a predictor enters / leaves for every category, k = (K−1)·(terms + 1) (K·(terms + 1) under `penalty`, which predict warns as above); candidates keep the first fit's categories (`stepAIC` on `multinom`) |
<!-- /feature -->

## Output

- `selected_features` — the chosen predictors in fit order; absent when none is kept.
- `coefficients` for selected predictors plus `(intercept)` only. **Absent ≠ zero** — non-selected predictors were dropped entirely.
- `std_errors` / `p_values` for selected predictors only. `selection` / `criterion` echo back.

## Gotchas

- Any non-empty `selection` forces the buffered path; predict reports it.
- `criterion` missing when `selection` set → `SERVICE_VALIDATION`.
- Scores = R's `AIC()` / `BIC()`: OLS Gaussian likelihood; binomial / poisson deviance + 2k (+ log(n)·k); gamma its likelihood at φ = deviance / n, φ counted (k + 1), so it walks R `step()`'s path where φ = 1 would not; NB each candidate's likelihood at its own θ̂ (θ counted; `stepAIC` on `glm.nb`). `step()` ranks moves by deviance / φ̂ of the current fit, then refits — rare ties can part.
- Worst-case `O(p²)` fits (`stepwise`); on wide tables prefer `forward`.
- Composes with `resample` — the resample wraps the selected-feature fit.
- Selection inflates type-I error on retained predictors; treat the model as exploratory.

## See

- `pulse_examples_search tags=[stepwise]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`, `op-reg-mod-resample`
