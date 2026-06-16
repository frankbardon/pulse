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

Regression operators emit coefficient + diagnostics; they do not produce Response.Components. This modifier shrinks the active predictor set in `Response.Regressions[i].SelectedFeatures` and drops non-selected entries from `Coefficients` / `StdErrors`.

## Params

Top-level `selection` field on `RegressionSpec` plus the required `criterion` companion.

| Name | Type | Default | Description |
|---|---|---|---|
| `selection` | enum | `""` | `""`, `forward`, `backward`, `stepwise` (bidirectional). |
| `criterion` | enum | required when set | `aic` or `bic`. |

Composition role: **wrapper, not a fit**. The engine refits the host model against candidate subsets and keeps the lowest-criterion winner.

| Value | Sweep |
|---|---|
| `forward` | start intercept-only; add the predictor that lowers `criterion` most each step |
| `backward` | start full; drop the predictor whose absence lowers `criterion` most each step |
| `stepwise` | bidirectional add/drop until no single move improves `criterion` |

## Inputs

Inherits `target` / `predictors` from the host `RegressionSpec`. No additional field inputs.

| Host | Accepted? |
|---|---|
| `REG_OLS` (`penalty == ""`) | yes |
| `REG_OLS` (`penalty != ""`) | rejected → `PROCESSING_REGRESSION_REGULARIZED_SELECTION` |
| `REG_GLM` | yes |
| `REG_BAYES_LINEAR` | rejected — stepwise on a NIG fit is out of scope |

## Output

`RegressionResult` reflects the search:

- `SelectedFeatures []string` — chosen predictors in fit order.
- `Coefficients` populated only for selected predictors plus `(intercept)`. **Absent ≠ zero** — non-selected predictors were dropped entirely.
- `StdErrors` / `PValues` emitted only for selected predictors.
- `Selection` / `Criterion` echo back.

## Gotchas

- Any non-empty `selection` forces buffered — `RegressionSpec.Streamable()` → false. Predict reports the downgrade.
- `criterion` required when `selection` set; missing → `SERVICE_VALIDATION`. `bic` penalizes complexity harder than `aic`.
- Worst-case `O(p²)` candidate fits (`stepwise`) on `p` predictors; on wide tables prefer `forward`.
- Composes with `resample` — the resample wraps the selected-feature fit.
- Selection inflates type-I error on retained predictors; treat the model as exploratory.

## See

- `pulse_examples_search tags=[stepwise]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`, `op-reg-mod-resample`
