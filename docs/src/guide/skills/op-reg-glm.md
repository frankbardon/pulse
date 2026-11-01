```yaml
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, gamma and negative binomial (NB2, theta estimated) families with family-specific link functions. Always buffered.
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

Flat keys (`params` is refused); shared: [`regression-spec`](regression-spec.md).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric. |
| `family` | enum | (required) | `binomial`, `poisson`, `gamma`, `negative_binomial`. |
| `link` | enum | — | `logit/probit/cloglog`; `log/identity/sqrt` (poisson, NB); `inverse/log/identity`; 1st default. |
| `theta` | float | — | NB fixed θ > 0; else ML. |
| `dispersion` | enum | `fixed` | NB + `theta`: `estimated` φ̂. |
| `penalty` | enum | `""` | `l2`, `l1`, `elasticnet` (binomial / poisson). |
| `alpha` | float | — | Strength, > 0. |
| `l1_ratio` | float | — | Mix, `0<r<1`. |
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
| `margins` | object | — | AME / MEM ([`regression-margins`](regression-margins.md)). |
| `weight` | slot weight | — | Prior weights. |
| `multiplicity` | object | — | p-values join a family. |
| `holdout` | expr | — | Held-out rows → `Holdout`. |
| `save` | string | — | Writes a model file. |
| `name` | string | — | Result / model name. |

## Inputs

See `## Contract`; binomial target `{0,1}` or `packed_bool`; null / NaN / ±Inf drops the row.

## Output

`RegressionResult`: `Coefficients` (`(intercept)` + βs, link scale), `StdErrors`, `PValues`, `CILower` / `CIUpper` — Wald z, no `DF`, or t on `DF` (gamma; [`regression-spec`](regression-spec.md)); `ConfLevel`, `Deviance`, `NullDeviance`, `PseudoR2`, `ConvergedIters`, `NObs`; `Family` / `Link` echoed. NB: `Theta`, `ThetaStdErr`, `TwoLogLik`, `AIC`. `l1` / `elasticnet` without `resample`: SE / p / CI null + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.

## Reading the output

- `coefficients.*`: The change in the linear predictor, on the link scale, for a one-unit increase in this predictor, holding the other predictors fixed: log-odds for binomial (logit link), the log of the expected count for Poisson (log link), the inverse of the expected value for gamma (inverse link), the log of the expected count for negative_binomial (log link).
- `p_values.*`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered; IRLS requires multiple passes over the data.

## Gotchas

- Binomial separation → `PROCESSING_REGRESSION_SEPARATION`; the slot fails alone. Add `penalty: l2`, or drop it.
- Other non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE`: raise `max_iters`; NB `theta_unbounded`: fit poisson. Another family's link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Poisson fixes φ = 1: overdispersed counts get too-small SEs; fit `negative_binomial` or a robust `covariance`.
- Collinear / constant → `PROCESSING_REGRESSION_RANK_DEFICIENT`. Other refusals: [`regression-spec`](regression-spec.md).

## See

- `pulse_examples_search tags=[glm]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-margins`](regression-margins.md), [`regression-inference`](regression-inference.md), [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md)
