---
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-specific link functions. Always buffered.
kind: operator
category: REG
operator: REG_GLM
type: reference
applies_to: process, compose, predict
examples_tags: [regression, glm, logistic, buffered-pipeline]
---

Fits ride `Response.Regressions[i]`; no `Response.Components`.

<!-- generated: use-when -->

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric predictor. |
| `family` | enum | (required) | `binomial`, `poisson`, `gamma`. |
| `link` | enum | — | `binomial→logit`, `poisson→log`, `gamma→inverse`. |
| `penalty` | enum | `""` | `l2`, `l1`, `elasticnet` (binomial / poisson). |
| `alpha` | float | — | Strength, > 0. |
| `l1_ratio` | float | — | Elastic-net mix, `0<r<1`. |
| `max_iters` | int | — | IRLS iteration cap. |
| `tol` | float | — | IRLS tolerance. |
| `resample` | enum | — | `bootstrap`, `jackknife`. |
| `bootstrap_iters` | int | `1000` | Resample. |
| `jackknife_groups` | int | — | Resample. |
| `jackknife_by` | field | — | Resample. |
| `rng_seed` | int | — | Resample. |
| `selection` | enum | — | `backward`, `forward`, `stepwise`. |
| `criterion` | enum | — | `aic`, `bic`. |
| `conf_level` | float | `0.95` | CI coverage. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`. |
| `covariance` | object | — | Robust SEs (`regression-inference`). |
| `weight` | slot weight | — | `null` opts out; IRLS prior weights. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Coefficient p-values join a family; adds `PAdjusted`. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Held-out rows → `Holdout`. |
<!-- /feature -->
<!-- feature: capability:model_save -->
| `save` | string | — | Writes a model file. |
<!-- /feature -->
| `name` | string | — | Result / model name. |

## Inputs

`target` / `predictors`: see `## Contract`; a binomial target is `{0,1}` or `packed_bool`; a null, NaN or ±Inf drops the row.

## Output

`RegressionResult`: `Coefficients` (`(intercept)` + βs, link scale), `StdErrors`, `PValues`, `CILower` / `CIUpper` — Wald z, no `DF` (binomial / poisson) or t on `DF` (gamma; `regression-spec`); `ConfLevel`, `Deviance`, `NullDeviance`, `PseudoR2`, `ConvergedIters`, `NObs`; `Family` / `Link` echoed. `l1` / `elasticnet` without `resample`: SE / p / CI null + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Binomial separation (a predictor splits the outcome, e.g. an empty 2×2 cell) → `PROCESSING_REGRESSION_SEPARATION` naming `details.predictors`; the slot fails alone. Add `penalty: l2`, or drop / merge it.
- Other non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE`: raise `max_iters`, rescale. Unsupported link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Binomial / poisson fix the dispersion at 1: overdispersed counts get too-small SEs — use a robust `covariance`.
- Collinear or constant predictor → `PROCESSING_REGRESSION_RANK_DEFICIENT` naming `details.columns`. Other refusals: `regression-spec`.

## See

- `pulse_examples_search tags=[glm]`
- Skills: `regression-spec`, `regression-modeling`, `regression-inference`, `op-reg-mod-resample`, `op-reg-mod-selection`
