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
| `reference` | string | — | Baseline; default first fitted level. |
| `penalty` | enum | `""` | `l2`, `l1`, `elasticnet`. |
| `alpha` | float | — | glmnet `lambda`. |
| `l1_ratio` | float | — | — |
| `unpenalized` | []field | — | — |
| `max_iters` | int | `50` | Newton cap. |
| `tol` | float | `0.00000001` | Relative step tolerance. |
| `conf_level` | float | `0.95` | Wald CI level. |
| `vcov` | bool | `false` | — |
| `weight` | slot weight | — | Frequency or probability. |
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
| `order` | object | — | Row order. |
| `save` | string | — | Model file. |
| `name` | string | — | Result name. |

## Inputs

`target`: a `categorical_*` cohort field (derived: `requires: categorical_target`). A null / non-finite predictor drops the row.

## Output

`Reference`, `Categories` (dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level; `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2`, `TwoLogLik`, `AIC`, `NObs`, `ConvergedIters`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- β is ITS level's log odds vs the reference; e^β a relative risk ratio.
- Empty levels drop; an empty named `reference` (`reference_empty`) or < 2 levels (`one_category`) fail alone.
- Refused (`PROCESSING_CONFIG`): `family` / `link` / `theta` / `dispersion`, `bounds`, a prior, `solver`, `intercept: false`, robust `covariance`, `margins`, `collinearity`.
- `penalty` (glmnet `ungrouped`): EVERY level gets coefficients, intercepts centred, no `reference`; null SE / p / CI + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` unless `resample`; `vcov` refused.
- `resample` / `selection` refit at the first fit's categories: a bootstrap draw missing one is skipped, a jackknife one fails (`replicate_empty_level`); a predictor moves for every category.
<!-- feature: ATTR_REG_PREDICT -->
- `ATTR_REG_PREDICT`: `category` → P(y = it); `output: class` → 0-based index in `Categories`; `scale: link` → log-odds vs the reference (penalized: x'β). `coef()` reads `"<level>:<term>"`.
<!-- /feature -->
- Probability weight: w·n_eff/Σw; no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: `regression-spec`, `regression-modeling`, `regression-inference`
