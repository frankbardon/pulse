```yaml
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-specific link functions. Always buffered.
kind: operator
category: REG
operator: REG_GLM
type: reference
applies_to: process, compose, predict
examples_tags: [regression, glm, logistic, buffered-pipeline]
```

Fits ride `Response.Regressions[i]`; no `Response.Components`.

## Use when

Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts.

Questions it answers:

- Which customer traits are associated with the chance of churning, holding the others fixed?
- How does the expected number of support tickets change with account size?

Use something else:

- `TEST_PROP_Z` when you only want to know whether a yes/no rate differs between two groups.

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: [`regression-spec`](regression-spec.md).

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
| `covariance` | object | — | Robust SEs ([`regression-inference`](regression-inference.md)). |
| `weight` | slot weight | — | `null` opts out; IRLS prior weights. |
| `multiplicity` | object | — | Coefficient p-values join a family; adds `PAdjusted`. |
| `holdout` | expr | — | Held-out rows → `Holdout`. |
| `save` | string | — | Writes a model file. |
| `name` | string | — | Result / model name. |

## Inputs

`target` / `predictors`: see `## Contract`; a binomial target is `{0,1}` or `packed_bool`; a null, NaN or ±Inf drops the row.

## Output

`RegressionResult`: `Coefficients` (`(intercept)` + βs, link scale), `StdErrors`, `PValues`, `CILower` / `CIUpper` — Wald z, no `DF` (binomial / poisson) or t on `DF` (gamma; [`regression-spec`](regression-spec.md)); `ConfLevel`, `Deviance`, `NullDeviance`, `PseudoR2`, `ConvergedIters`, `NObs`; `Family` / `Link` echoed. `l1` / `elasticnet` without `resample`: SE / p / CI null + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.

## Reading the output

- `coefficients.*`: The change in the linear predictor, on the link scale, for a one-unit increase in this predictor, holding the other predictors fixed: log-odds for binomial (logit link), the log of the expected count for Poisson (log link), the inverse of the expected value for gamma (inverse link).
- `std_errors.*`: How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix: with the dispersion fixed at 1 for binomial and Poisson, and scaled by the estimated dispersion for gamma.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered; IRLS requires multiple passes over the data.

## Gotchas

- Binomial separation (a predictor splits the outcome, e.g. an empty 2×2 cell) → `PROCESSING_REGRESSION_SEPARATION` naming `details.predictors`; the slot fails alone. Add `penalty: l2`, or drop / merge it.
- Other non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE`: raise `max_iters`, rescale. Unsupported link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Binomial / poisson fix the dispersion at 1: overdispersed counts get too-small SEs — use a robust `covariance`.
- Collinear or constant predictor → `PROCESSING_REGRESSION_RANK_DEFICIENT` naming `details.columns`. Other refusals: [`regression-spec`](regression-spec.md).

## See

- `pulse_examples_search tags=[glm]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-inference`](regression-inference.md), [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md)
