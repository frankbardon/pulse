```yaml
name: op-reg-mod-selection
description: Spec-level subset-selection modifier (forward / backward / stepwise) that composes with an OLS or GLM fit; drives a greedy search over predictors against an information criterion. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_SELECTION
type: reference
applies_to: process, compose, predict
examples_tags: [regression, selection, stepwise, buffered-pipeline]
```

Regression operators emit coefficient + diagnostics; no `Response.Components`. This modifier shrinks the active predictor set in `Response.Regressions[i].SelectedFeatures` and drops non-selected entries from `Coefficients` / `StdErrors`.

## Params

Top-level `selection` on `RegressionSpec` plus its required `criterion` companion. A **wrapper, not a fit**: the engine refits the host model against candidate subsets and keeps the lowest-criterion winner.

| Name | Type | Default | Description |
|---|---|---|---|
| `selection` | `backward` \| `forward` \| `stepwise` | — | `forward` (intercept-only, add best each step), `backward` (full, drop best each step), `stepwise` (bidirectional until no move improves); absent / `""` = off. |
| `criterion` | `aic` \| `bic` | — | Information criterion; required when `selection` is set (`SERVICE_VALIDATION`). |

## Inputs

Inherits `target` / `predictors` from the host `RegressionSpec`. No additional field inputs.

| Host | Accepted? |
|---|---|
| `REG_OLS` (`penalty == ""`) | yes |
| `REG_OLS` (`penalty != ""`) | → `PROCESSING_REGRESSION_REGULARIZED_SELECTION` |
| `REG_GLM` (`penalty == ""`) | yes |
| `REG_GLM` (`penalty != ""`) | → `PROCESSING_REGRESSION_REGULARIZED_SELECTION` |
| `REG_BAYES_LINEAR` | rejected — stepwise on a NIG fit is out of scope |

## Output

- `SelectedFeatures []string` — chosen predictors in fit order.
- `Coefficients` for selected predictors plus `(intercept)` only. **Absent ≠ zero** — non-selected predictors were dropped entirely.
- `StdErrors` / `PValues` for selected predictors only. `Selection` / `Criterion` echo back.

## Gotchas

- Any non-empty `selection` forces buffered — `RegressionSpec.Streamable()` → false. Predict reports the downgrade.
- `criterion` missing when `selection` set → `SERVICE_VALIDATION`. `bic` penalizes complexity harder than `aic`.
- Scores = R's `AIC()` / `BIC()`: OLS Gaussian likelihood; binomial / poisson deviance + 2k (+ log(n)·k); gamma its likelihood at φ = deviance / n, φ counted (k + 1), so it walks R `step()`'s path where φ = 1 would not. `step()` ranks moves by deviance / φ̂ of the current fit, then refits — rare ties can part.
- Worst-case `O(p²)` fits (`stepwise`); on wide tables prefer `forward`.
- Composes with `resample` — the resample wraps the selected-feature fit.
- Selection inflates type-I error on retained predictors; treat the model as exploratory.

## See

- `pulse_examples_search tags=[stepwise]`
- Skills: [`regression-modeling`](regression-modeling.md), [`op-reg-ols`](op-reg-ols.md), [`op-reg-glm`](op-reg-glm.md), [`op-reg-mod-resample`](op-reg-mod-resample.md)
