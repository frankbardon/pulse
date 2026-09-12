---
name: op-reg-mod-resample
description: Spec-level resampling modifier (jackknife / bootstrap) that composes with REG_OLS or REG_GLM; replaces analytical std errors with empirical estimates. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_RESAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, resampling, jackknife, buffered-pipeline]
---

Regression operators emit coefficient + diagnostics, not Response.Components. This modifier overwrites `StdErrors` / `PValues` in `Response.Regressions[i]`; the point estimate stays the full-data fit.

## Params

Top-level `resample` on `RegressionSpec` plus two bootstrap knobs. A **wrapper, not a fit**: the host `REG_OLS` / `REG_GLM` runs once per replicate (jackknife = `n` refits; bootstrap = `bootstrap_iters`) and β samples are aggregated.

| Name | Type | Default | Description |
|---|---|---|---|
| `resample` | enum | `""` | `""`, `jackknife` (leave-one-out), `bootstrap` (non-parametric). |
| `bootstrap_iters` | int | `1000` | Replicate count; read only when `resample == "bootstrap"`. |
| `rng_seed` | int | `0` | `0` derives a deterministic per-request seed; non-zero is reproducible. |

## Inputs

Inherits `target` / `predictors` from the host `RegressionSpec`. No additional field inputs.

| Host | Accepted? |
|---|---|
| `REG_OLS` (any penalty) | yes |
| `REG_GLM` | yes |
| `REG_BAYES_LINEAR` | rejected — credible intervals already convey uncertainty |

## Output

- `StdErrors[name]` — jackknife `√((n−1)/n · Σ(β⁽⁻ⁱ⁾ − β̄)²)`; bootstrap sample std of replicates.
- `PValues[name]` — jackknife pivotal; bootstrap percentile-method two-sided.
- `Resample` echoes back; `Coefficients` unchanged. For `l1` / `elasticnet` OLS, bootstrap suppresses `PROCESSING_REGRESSION_APPROXIMATE_SE`.

## Gotchas

- Any non-empty `resample` forces buffered — `RegressionSpec.Streamable()` → false. Predict reports the downgrade.
- Jackknife is `O(n)` refits; on large cohorts prefer bootstrap with moderate replicates.
- Composes with `selection` — the resample wraps the selected-feature fit.
- `rng_seed = 0` is deterministic per request but cohort-dependent; pin non-zero for cross-cohort reproducibility.

## See

- `pulse_examples_search tags=[jackknife]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`, `op-reg-mod-selection`
