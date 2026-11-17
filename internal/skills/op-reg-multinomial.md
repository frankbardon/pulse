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
| `covariance` | object | — | `hc0`, `hc1`, `cluster`. |
| `exponentiate` | bool | `false` | — |
| `margins` | object | — | `regression-margins`. |
| `term_tests` | bool | `false` | — |
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

`Reference`, `Categories`; `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level; `Deviance`, `NullDeviance`, `PseudoR2`, `TwoLogLik`, `AIC`. `exponentiate`: `Exponentiated` (e^β, e^β·SE, e^CI). `term_tests`: `TermTests`, per predictor on df K−1 `wald_*` (the fit's covariance) and `lr_*` (vs the refit without it).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- β: ITS level's log odds vs the reference.
- Empty levels drop; an empty `reference` (`reference_empty`) or one level (`one_category`) fails alone.
- Refused (`PROCESSING_CONFIG`): GLM knobs, `bounds`, a prior, `solver`, `intercept: false`, `collinearity`; `margins` / `term_tests` beside a modifier or `penalty`.
- `penalty` (glmnet `ungrouped`): EVERY level gets coefficients, no `reference`; null SE / p / CI unless `resample`; no `vcov`.
- `resample` / `selection` keep the fit's categories: a bootstrap draw missing one is skipped, a jackknife one fails (`replicate_empty_level`).
<!-- feature: ATTR_REG_PREDICT -->
- `ATTR_REG_PREDICT`: `category` → P(y = it); `output: class` → index in `Categories`; `scale: link` → log-odds (penalized: x'β). `coef()` reads `"<level>:<term>"`.
<!-- /feature -->
- Probability weight: no `TwoLogLik` / `AIC` / `lr_*`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: `regression-spec`, `regression-modeling`, `regression-inference`
