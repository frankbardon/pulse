---
name: op-reg-multinomial
description: Multinomial (baseline-category) logistic regression of a categorical target on numeric predictors; one coefficient set per non-reference level, log odds against the reference. Always buffered.
kind: operator
category: REG
operator: REG_MULTINOMIAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, multinomial, buffered-pipeline]
---

Rides `Response.Regressions[i]`; no `Response.Components`.

<!-- generated: use-when -->

## Params

Flat keys (`params` is refused); shared: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Categorical; dictionary order. |
| `predictors` | []field | (required) | ≥1 numeric. |
| `reference` | string | — | First fitted level. |
| `penalty` | enum | `""` | `l2`, `l1`, `elasticnet`. |
| `alpha` | float | — | — |
| `l1_ratio` | float | — | — |
| `unpenalized` | []field | — | — |
| `max_iters` | int | `50` | — |
| `tol` | float | `0.00000001` | — |
| `conf_level` | float | `0.95` | — |
| `vcov` | bool | `false` | — |
| `covariance` | object | — | `hc0`, `hc1`, `cluster`: `regression-inference`. |
| `exponentiate` | bool | `false` | — |
| `weight` | slot weight | — | — |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Every non-intercept key. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Proper scores: `regression-row-scores`. |
<!-- /feature -->
| `resample` | enum | — | `op-reg-mod-resample`. |
| `bootstrap_iters` | int | `1000` | — |
| `jackknife_groups` | int | — | — |
| `jackknife_by` | field | — | — |
| `rng_seed` | int | — | — |
| `selection` | enum | — | `op-reg-mod-selection`. |
| `criterion` | enum | — | `aic`, `bic`. |
| `order` | object | — | — |
| `save` | string | — | Model file. |
| `name` | string | — | — |

## Inputs

`target`: a `categorical_*` cohort field (derived: `requires: categorical_target`). A null predictor drops the row.

## Output

`Reference`, `Categories` (dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level; `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2`, `TwoLogLik`, `AIC`, `NObs`, `ConvergedIters`. `exponentiate` adds `Exponentiated` {`estimates` e^β, `std_errors` e^β·SE, `ci_*` e^bound}.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- β is ITS level's log odds vs the reference; e^β (`exponentiate`) a relative risk ratio.
- Empty levels drop; an empty named `reference` (`reference_empty`) or < 2 levels (`one_category`) fail alone.
- Refused (`PROCESSING_CONFIG`): GLM knobs, `bounds`, a prior, `solver`, `intercept: false`, `margins`, `collinearity`.
- `penalty` (glmnet `ungrouped`): EVERY level gets coefficients, no `reference`; null SE / p / CI (advisory) unless `resample`; `vcov` refused.
- `resample` / `selection` refit at the first fit's categories: a bootstrap draw missing one is skipped, a jackknife one fails (`replicate_empty_level`); a predictor moves for every category.
<!-- feature: ATTR_REG_PREDICT -->
- `ATTR_REG_PREDICT`: `category` → P(y = it); `output: class` → 0-based index in `Categories`; `scale: link` → log-odds vs the reference (penalized: x'β). `coef()` reads `"<level>:<term>"`.
<!-- /feature -->
- Probability weight (w·n_eff/Σw): no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: `regression-spec`, `regression-modeling`, `regression-inference`
