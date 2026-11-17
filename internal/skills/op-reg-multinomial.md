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

Fits ride `Response.Regressions[i]`; no `Response.Components`.

<!-- generated: use-when -->

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Categorical cohort field; dictionary order = category order. |
| `predictors` | []field | (required) | ≥1 numeric. |
| `reference` | string | — | Baseline label; absent: first fitted level. |
| `max_iters` | int | `50` | Newton cap. |
| `tol` | float | `0.00000001` | Relative step tolerance. |
| `conf_level` | float | `0.95` | Wald CI level. |
| `vcov` | bool | `false` | Inverse information, `Correlation`. |
| `weight` | slot weight | — | Frequency or probability. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Default: every non-intercept `<level>:<term>` key. |
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
| `save` | string | — | Model file (`regression-row-scores`). |
| `name` | string | — | Result name. |

## Inputs

`target`: a `categorical_*` cohort field (numeric: `PULSE_FIELD_TYPE_UNSUPPORTED`; derived: `requires: categorical_target`). A null or non-finite predictor drops the row.

## Output

`Reference`, `Categories` (fitted levels, dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level (intercept `(intercept)`); `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2` (McFadden), `TwoLogLik`, `AIC`, `NObs`, `ConvergedIters`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A coefficient is ITS level's log odds vs the reference; e^β is a relative risk ratio.
- An empty level is dropped; an empty named `reference` (`reference_empty`) or < 2 fitted levels (`one_category`) fail the slot alone (`INSUFFICIENT_DATA`).
- Refused (`PROCESSING_CONFIG`): `family` / `link` / `theta` / `dispersion`, `penalty`, `bounds`, a prior, `solver`, `intercept: false`, a robust `covariance`, `margins`, `collinearity`.
- `resample` / `selection` refit at the first fit's categories and reference: a bootstrap draw missing one is skipped, a jackknife one fails (`replicate_empty_level`). A predictor moves for every category: k = (K−1)·(terms + 1).
<!-- feature: ATTR_REG_PREDICT -->
- Bound, `ATTR_REG_PREDICT` scores a row: `category` → P(y = it); `output: class` → 0-based index in `Categories`; `scale: link` → log-odds vs the reference. `coef()` reads `"<level>:<term>"`.
<!-- /feature -->
- Probability weight: multinom on w* = w·n_eff/Σw; no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: `regression-spec`, `regression-modeling`, `regression-inference`
