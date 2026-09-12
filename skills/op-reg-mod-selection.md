---
name: op-reg-mod-selection
description: Spec-level subset-selection modifier (forward / backward / stepwise) that composes with REG_OLS or REG_GLM; drives a greedy search over predictors against an information criterion. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_SELECTION
type: reference
applies_to: process, compose, predict
examples_tags: [regression, selection, stepwise, buffered-pipeline]
---

Regression operators emit coefficient + diagnostics, not Response.Components. This modifier shrinks the active predictor set in `Response.Regressions[i].SelectedFeatures` and drops non-selected entries from `Coefficients` / `StdErrors`.

## Params

Top-level `selection` on `RegressionSpec` plus its required `criterion` companion. A **wrapper, not a fit**: the engine refits the host model against candidate subsets and keeps the lowest-criterion winner.

- `selection` — enum, default `""`. `""`, `forward` (intercept-only, add best each step), `backward` (full, drop best each step), `stepwise` (bidirectional add/drop until no move improves).
- `criterion` — enum, default required when set. `aic` or `bic`.

## Inputs

Inherits `target` / `predictors` from the host `RegressionSpec`. No additional field inputs.

| Host | Accepted? |
|---|---|
| `REG_OLS` (`penalty == ""`) | yes |
| `REG_OLS` (`penalty != ""`) | → `PROCESSING_REGRESSION_REGULARIZED_SELECTION` |
| `REG_GLM` | yes |
| `REG_BAYES_LINEAR` | rejected — stepwise on a NIG fit is out of scope |

## Output

- `SelectedFeatures []string` — chosen predictors in fit order.
- `Coefficients` for selected predictors plus `(intercept)` only. **Absent ≠ zero** — non-selected predictors were dropped entirely.
- `StdErrors` / `PValues` for selected predictors only. `Selection` / `Criterion` echo back.

## Gotchas

- Any non-empty `selection` forces buffered — `RegressionSpec.Streamable()` → false. Predict reports the downgrade.
- `criterion` missing when `selection` set → `SERVICE_VALIDATION`. `bic` penalizes complexity harder than `aic`.
- Worst-case `O(p²)` fits (`stepwise`); on wide tables prefer `forward`.
- Composes with `resample` — the resample wraps the selected-feature fit.
- Selection inflates type-I error on retained predictors; treat the model as exploratory.

## See

- `pulse_examples_search tags=[stepwise]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`, `op-reg-mod-resample`
