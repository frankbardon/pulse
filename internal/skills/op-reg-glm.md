---
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, gamma and negative binomial (NB2, theta estimated) families with family-specific link functions. Always buffered.
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

Flat keys (`params` is refused); shared: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric. |
| `family` | enum | (required) | `binomial`, `poisson`, `gamma`, `negative_binomial`. |
| `link` | enum | — | `logit/probit/cloglog`; `log/identity/sqrt` (poisson, NB); `inverse/log/identity`; 1st default. |
| `theta` | float | — | NB fixed θ > 0; else ML. |
| `penalty` | enum | `""` | `l2`, `l1`, `elasticnet` (binomial / poisson). |
| `alpha` | float | — | Strength, > 0. |
| `l1_ratio` | float | — | Elastic-net mix, `0<r<1`. |
| `max_iters` | int | — | IRLS cap. |
| `tol` | float | — | IRLS tolerance. |
| `resample` | enum | — | `bootstrap`, `jackknife`. |
| `bootstrap_iters` | int | `1000` | Resample. |
| `jackknife_groups` | int | — | Resample. |
| `jackknife_by` | field | — | Resample. |
| `rng_seed` | int | — | Resample. |
| `selection` | enum | — | `backward`, `forward`, `stepwise`. |
| `criterion` | enum | — | `aic`, `bic`. |
| `conf_level` | float | `0.95` | CI level. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`. |
| `covariance` | object | — | Robust SEs. |
| `margins` | object | — | AME / MEM (`regression-margins`). |
| `weight` | slot weight | — | Prior weights. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | p-values join a family. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Held-out rows → `Holdout`. |
<!-- /feature -->
<!-- feature: capability:model_save -->
| `save` | string | — | Writes a model file. |
<!-- /feature -->
| `name` | string | — | Result / model name. |

## Inputs

See `## Contract`; binomial target `{0,1}` or `packed_bool`; null / NaN / ±Inf drops the row.

## Output

`RegressionResult`: `Coefficients` (`(intercept)` + βs, link scale), `StdErrors`, `PValues`, `CILower` / `CIUpper` — Wald z, no `DF`, or t on `DF` (gamma; `regression-spec`); `ConfLevel`, `Deviance`, `NullDeviance`, `PseudoR2`, `ConvergedIters`, `NObs`; `Family` / `Link` echoed. NB: `Theta`, `ThetaStdErr`, `TwoLogLik`, `AIC`. `l1` / `elasticnet` without `resample`: SE / p / CI null + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Binomial separation → `PROCESSING_REGRESSION_SEPARATION` (`details.predictors`); the slot fails alone. Add `penalty: l2`, or drop it.
- Other non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE`: raise `max_iters`, rescale; NB `theta_unbounded`: fit poisson. Another family's link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Poisson fixes dispersion at 1: overdispersed counts get too-small SEs; fit `negative_binomial` or a robust `covariance`.
- Collinear / constant → `PROCESSING_REGRESSION_RANK_DEFICIENT` (`details.columns`). Other refusals: `regression-spec`.

## See

- `pulse_examples_search tags=[glm]`
- Skills: `regression-spec`, `regression-modeling`, `regression-margins`, `regression-inference`, `op-reg-mod-resample`, `op-reg-mod-selection`
