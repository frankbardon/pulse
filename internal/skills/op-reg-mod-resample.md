---
name: op-reg-mod-resample
description: Spec-level resampling modifier (jackknife — leave-one-out, grouped or by cluster — / bootstrap) that composes with an OLS or GLM fit; replaces analytical std errors with empirical estimates. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_RESAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, resampling, jackknife, buffered-pipeline]
---

Regression operators emit coefficient + diagnostics; no `Response.Components`. This modifier overwrites `StdErrors` / `PValues` in `Response.Regressions[i]`; the point estimate stays the full-data fit.

<!-- generated: use-when -->

## Params

Top-level `resample` on `RegressionSpec` plus its knobs. A **wrapper, not a fit**: the host fit runs once per replicate (leave-one-out = `n` refits; delete-a-group = `g`; bootstrap = `bootstrap_iters`) and β samples are aggregated.

| Name | Type | Default | Description |
|---|---|---|---|
| `resample` | enum | `""` | `""`, `jackknife`, `bootstrap` (non-parametric). |
| `jackknife_groups` | int | absent | Jackknife only: delete-a-group over `g` seeded random groups (2 ≤ g ≤ fitted rows, sizes within one). Absent = leave-one-out. |
| `jackknife_by` | field | absent | Jackknife only: delete-a-cluster, one replicate per distinct value (≥ 2); a null value drops the row from the fit. Exclusive with `jackknife_groups` (`PROCESSING_CONFIG`). |
| `bootstrap_iters` | int | `1000` | Replicate count, used as given (`<= 0` → `1000`); read only when `resample == "bootstrap"`. |
| `rng_seed` | int | `0` | Seeds the bootstrap and the `jackknife_groups` draw; `0` derives a deterministic per-request seed; non-zero is reproducible. |

## Inputs

Inherits `target` / `predictors` from the host `RegressionSpec`. No additional field inputs.

| Host | Accepted? |
|---|---|
<!-- feature: REG_OLS -->
| `REG_OLS` (any penalty) | yes |
<!-- /feature -->
<!-- feature: REG_GLM -->
| `REG_GLM` (any penalty) | yes |
<!-- /feature -->
<!-- feature: REG_BAYES_LINEAR -->
| `REG_BAYES_LINEAR` | rejected — credible intervals already convey uncertainty |
<!-- /feature -->

## Output

- `StdErrors[name]` — jackknife `√((g−1)/g · Σ(β₍ₖ₎ − β̄)²)` over its `g` replicates (leave-one-out: `g` = the fitted rows; a row the fit drops is no replicate); bootstrap sample std of replicates.
- A delete-a-group jackknife echoes `jackknife_groups` (`g`; the distinct `jackknife_by` values); leave-one-out omits it.
- `PValues[name]` — jackknife pivotal; bootstrap percentile-method two-sided.
- `Resample` echoes back; `Coefficients` unchanged. For `l1` / `elasticnet` (OLS or GLM), a resample replaces the null SE / p with replicate ones and silences `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`; a penalty refits every replicate penalized.
- Bootstrap echoes `bootstrap_requested` (replicates run) and `bootstrap_succeeded` (refits that succeeded; SE / p / vcov use only these). A failed refit is skipped, so `succeeded < requested` flags an unstable fit; under 10 successes is `PROCESSING_REGRESSION_NO_CONVERGE`.

## Gotchas

- Any non-empty `resample` forces buffered — `RegressionSpec.Streamable()` → false. Predict reports the downgrade.
- Leave-one-out is `n` refits — cost grows with n²; predict's `PULSE_ADVISORY_JACKKNIFE_COST` fires at ≥ 10,000 records. Use `jackknife_groups: 50` (or a natural cluster in `jackknife_by`).
- Every jackknife replicate must refit: one failure (e.g. separation once a group is gone) fails the slot with that refit's code, `details.jackknife_group` naming it. `g` above the fitted rows, or one cluster, is `PROCESSING_REGRESSION_INSUFFICIENT_DATA`.
- Composes with `selection` — the resample wraps the selected-feature fit.
- `rng_seed = 0` is deterministic per request but cohort-dependent; pin non-zero for cross-cohort reproducibility.
- Jackknife is weighted (JK1): each replicate refits the remaining rows with their own weights. Bootstrap is never weighted: a weight in force is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[jackknife]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`, `op-reg-mod-selection`
