```yaml
name: op-reg-multinomial
description: Multinomial (baseline-category) logistic regression of a categorical target on numeric predictors; one coefficient set per non-reference level, log odds against the reference. Always buffered.
kind: operator
category: REG
operator: REG_MULTINOMIAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, multinomial, buffered-pipeline]
```

Rides `Response.Regressions[i]`; no `Response.Components`.

## Use when

Fits a model of an outcome with three or more categories (which plan, brand or channel), comparing each with a reference category.

Questions it answers:

- Which customer traits are associated with choosing the premium or the basic plan over the standard one, holding the others fixed?
- How does the chance of each support channel being picked change with the customer's age?

## Params

Flat keys (`params` is refused); shared: [`regression-spec`](regression-spec.md).

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
| `multiplicity` | object | — | Every non-intercept key. |
| `holdout` | expr | — | Proper scores: [`regression-row-scores`](regression-row-scores.md). |
| `resample` | enum | — | [`op-reg-mod-resample`](op-reg-mod-resample.md). |
| `bootstrap_iters` | int | `1000` | — |
| `jackknife_groups` | int | — | — |
| `jackknife_by` | field | — | — |
| `rng_seed` | int | — | — |
| `selection` | enum | — | [`op-reg-mod-selection`](op-reg-mod-selection.md). |
| `criterion` | enum | — | `aic`, `bic`. |
| `order` | object | — | Row order. |
| `save` | string | — | Model file. |
| `name` | string | — | Result name. |

## Inputs

`target`: a `categorical_*` cohort field (derived: `requires: categorical_target`). A null / non-finite predictor drops the row.

## Output

`Reference`, `Categories` (dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level; `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2`, `TwoLogLik`, `AIC`, `NObs`, `ConvergedIters`.

## Reading the output

- `coefficients.*`: Keyed &lt;category&gt;:&lt;term&gt;: the change in the log odds of that category against the reference category for a one-unit increase in the term's predictor, holding the other predictors fixed; &lt;category&gt;:(intercept) is that log odds when every predictor is 0.
- `std_errors.*`: How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix (nnet::multinom's Hessian).
- `conf_level`: The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

## Contract

- Types (`target`): `categorical_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered; Newton-Raphson requires multiple passes over the data.

## Gotchas

- β is ITS level's log odds vs the reference; e^β a relative risk ratio.
- Empty levels drop; an empty named `reference` (`reference_empty`) or < 2 levels (`one_category`) fail alone.
- Refused (`PROCESSING_CONFIG`): `family` / `link` / `theta` / `dispersion`, `bounds`, a prior, `solver`, `intercept: false`, robust `covariance`, `margins`, `collinearity`.
- `penalty` (glmnet `ungrouped`): EVERY level gets coefficients, intercepts centred, no `reference`; null SE / p / CI + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` unless `resample`; `vcov` refused.
- `resample` / `selection` refit at the first fit's categories: a bootstrap draw missing one is skipped, a jackknife one fails (`replicate_empty_level`); a predictor moves for every category.
- `ATTR_REG_PREDICT`: `category` → P(y = it); `output: class` → 0-based index in `Categories`; `scale: link` → log-odds vs the reference (penalized: x'β). `coef()` reads `"<level>:<term>"`.
- Probability weight: w·n_eff/Σw; no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-inference`](regression-inference.md)
