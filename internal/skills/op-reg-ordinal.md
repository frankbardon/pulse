---
name: op-reg-ordinal
description: REG_ORDINAL — ordinal (proportional-odds) regression of an ordered response, MASS::polr's model, logit / probit / cloglog; thresholds, Brant test, weights, refusals. Always buffered.
kind: operator
category: REG
operator: REG_ORDINAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ordinal]
---

`REG_ORDINAL` fits `P(Y ≤ k | x) = F(ζ_k − xᵀβ)` (polr): one coefficient per predictor shifts every threshold alike.

<!-- generated: use-when -->

## Params

`link` / `levels` / `brant` sit under `params`; shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Categorical or numeric, 3 to 100 levels. |
| `predictors` | []field | (required) | — |
| `link` | enum | `logit` | `logit`, `probit`, `cloglog`. |
| `levels` | array | — | Level order, lowest first. |
| `brant` | object | — | `{}` adds `outputs.brant`; logit only. |
| `conf_level` | float | `0.95` | CI level. |
| `vcov` | bool | `false` | — |
| `covariance` | object | — | `hc0`, `hc1`, `cluster`. |
| `exponentiate` | bool | `false` | — |
| `margins` | object | — | `regression-margins`. |
| `weight` | object | — | — |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Predictors' p-values only. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Proper scores. |
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

A categorical response reads its labels; a numeric one formats its values (`"3"`, `"2.5"`). A row whose response `levels` omits is dropped, as is a null cluster. Too few / many levels on the fitted rows, or a listed level no row carries, fails the slot alone (`INSUFFICIENT_DATA`, `reason`).

## Output

`coefficients`: predictors (spec order), then thresholds `"<level>|<next>"` (no intercept). `p_values` predictors only, Wald `ci_*`; `deviance` = −2ℓ, `null_deviance` (thresholds only), `pseudo_r2`, `two_log_lik` / `aic` (absent under a probability weight). `outputs`: `link`, `levels`, `thresholds`, `fitted_probabilities` (mean P per level), `brant` (omnibus df (K − 2)·p, per-predictor `terms` K − 2). `exponentiate` adds `exponentiated` {e^β, e^β·SE, `ci_*` e^bound}, predictors only; `margins`: dP(level)/dx per `"<level>:<term>"`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Dictionary order = import order; else pass `levels`.
- Small Brant p: the predictor's effect differs across thresholds.
- Frequency weights = the `rep()` expansion; probability weights follow<!-- feature: REG_GLM --> `REG_GLM`'s rule<!-- /feature --> (w* = w·n_eff/Σw).
- Refused, predict too (`PROCESSING_CONFIG`, `details.slot`): `penalty`, `collinearity`, `hc2` / `hc3` / `hac`, top-level `link` / `family`.
- `resample` / `selection` keep the first fit's levels: a bootstrap draw lacking one is skipped, a jackknife one fails (`replicate_empty_level`). AIC / BIC k counts thresholds.
- Separation: `NO_CONVERGE`; fails alone.

## See

`regression-modeling` · `regression-spec` · `op-reg-glm` · `regression-inference` · `weighting`
