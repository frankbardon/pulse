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

Fits ride `Response.Regressions[i]`; no `Response.Components`.

## Use when

Fits a model of an outcome with three or more categories (which plan, brand or channel), comparing each with a reference category.

Questions it answers:

- Which customer traits are associated with choosing the premium or the basic plan over the standard one, holding the others fixed?
- How does the chance of each support channel being picked change with the customer's age?

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: [`regression-spec`](regression-spec.md).

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
| `multiplicity` | object | — | Default: every non-intercept `<level>:<term>` key. |
| `holdout` | expr | — | Proper scores: [`regression-row-scores`](regression-row-scores.md). |
| `resample` | enum | — | [`op-reg-mod-resample`](op-reg-mod-resample.md). |
| `bootstrap_iters` | int | `1000` | — |
| `jackknife_groups` | int | — | — |
| `jackknife_by` | field | — | — |
| `rng_seed` | int | — | — |
| `selection` | enum | — | [`op-reg-mod-selection`](op-reg-mod-selection.md). |
| `criterion` | enum | — | `aic`, `bic`. |
| `order` | object | — | Row order. |
| `save` | string | — | Model file ([`regression-row-scores`](regression-row-scores.md)). |
| `name` | string | — | Result name. |

## Inputs

`target`: a `categorical_*` cohort field (numeric: `PULSE_FIELD_TYPE_UNSUPPORTED`; derived: `requires: categorical_target`). A null or non-finite predictor drops the row.

## Output

`Reference`, `Categories` (fitted levels, dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level (intercept `(intercept)`); `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2` (McFadden), `TwoLogLik`, `AIC`, `NObs`, `ConvergedIters`.

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

- A coefficient is ITS level's log odds vs the reference; e^β is a relative risk ratio.
- An empty level is dropped; an empty named `reference` (`reference_empty`) or < 2 fitted levels (`one_category`) fail the slot alone (`INSUFFICIENT_DATA`).
- Refused (`PROCESSING_CONFIG`): `family` / `link` / `theta` / `dispersion`, `penalty`, `bounds`, a prior, `solver`, `intercept: false`, a robust `covariance`, `margins`, `collinearity`.
- `resample` / `selection` refit at the first fit's categories and reference: a bootstrap draw missing one is skipped, a jackknife one fails (`replicate_empty_level`). A predictor moves for every category: k = (K−1)·(terms + 1).
- Bound, `ATTR_REG_PREDICT` scores a row: `category` → P(y = it); `output: class` → 0-based index in `Categories`; `scale: link` → log-odds vs the reference. `coef()` reads `"<level>:<term>"`.
- Probability weight: multinom on w* = w·n_eff/Σw; no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-inference`](regression-inference.md)
