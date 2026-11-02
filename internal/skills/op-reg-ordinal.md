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

`REG_ORDINAL` fits `P(Y ≤ k | x) = F(ζ_k − xᵀβ)` over the response's ordered levels (polr): one coefficient per predictor shifts every threshold alike.

<!-- generated: use-when -->

## Params

`link` / `levels` / `brant` sit under `params`; shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Ordered response: categorical or numeric, 3 to 100 levels. |
| `predictors` | []field | (required) | Numeric predictors. |
| `link` | enum | `logit` | `logit`, `probit`, `cloglog` (polr's method). |
| `levels` | array | — | Level order, lowest first (labels, or values as written). Absent: dictionary order / ascending values. |
| `brant` | object | — | `{}` adds `outputs.brant`; logit only. |
| `conf_level` | float | `0.95` | Wald CI coverage. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`. |
| `covariance` | object | — | `hc0`, `hc1`, `cluster`; `hc2` / `hc3` / `hac` refused. |
| `weight` | object | — | Frequency or probability. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Predictors' p-values join a family (thresholds carry none). |
<!-- /feature -->
| `name` | string | — | Result name. |

## Inputs

A categorical response reads its labels; a numeric one formats its values (`"3"`, `"2.5"`). A row whose response `levels` does not list is dropped (R's `factor(y, levels =)` NA), as is a null cluster. Fewer than 3 or more than 100 levels on the fitted rows, or a listed level no row carries, fails the slot alone (`PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `reason`).

## Output

`coefficients`: predictors (spec order), then thresholds `"<level>|<next>"` (no intercept). `std_errors` from the inverse observed information (or the sandwich), `statistics` z, `p_values` predictors only, Wald `ci_*`; `deviance` = −2ℓ, `null_deviance` (thresholds only), `pseudo_r2`, `two_log_lik` / `aic` (absent under a probability weight). `outputs`: `link`, `levels`, `thresholds`, `fitted_probabilities` (mean fitted P per level), `brant` `{statistic, df, p_value, terms}` — omnibus df (K − 2)·p, per predictor K − 2.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Positive β: higher x goes with higher levels; logit `exp(β)` is the odds ratio of being above any level.
- Dictionary order is import order, not meaning: pass `levels` for a scale imported out of order.
- A small Brant p-value says that predictor's effect differs across thresholds; the omnibus uses the symmetric covariance (the `brant` package's differs, its per-term figures agree).
- Frequency weights = the `rep()` expansion; probability weights follow<!-- feature: REG_GLM --> `REG_GLM`'s rule<!-- /feature --> (w* = w·n_eff/Σw).
- Refused at predict and run alike (`PROCESSING_CONFIG`, `details.slot`): `penalty`, `selection`, `resample`, `margins`, `collinearity`, top-level `link` / `family`, `save`; `holdout` is `PULSE_MODEL_SCORE_INVALID` (no scorer).
- Separation is `PROCESSING_REGRESSION_NO_CONVERGE`; the slot fails alone.

## See

`regression-modeling` · `regression-spec` · `op-reg-glm` · `regression-inference` · `weighting`
