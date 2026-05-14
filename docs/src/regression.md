# Regression Modeling

Pulse exposes regression through a compact, composable surface. Three operators, two orthogonal modifiers, and one upstream feature transform together cover every textbook regression variant. This chapter is the human-facing counterpart to `skills/regression-modeling.md`; agents should fetch the skill via `pulse_skills_get` rather than read this page.

## Overview

| Operator           | Engine                                       | Streaming                                  |
|--------------------|----------------------------------------------|--------------------------------------------|
| `REG_OLS`          | Ordinary least squares + optional regularization | Streams sufficient statistics (Phase 1 + 2) |
| `REG_GLM`          | Generalized linear model via IRLS            | Always buffered (Newton-Raphson refit)     |
| `REG_BAYES_LINEAR` | Bayesian linear regression (conjugate NIG)   | Streams sufficient statistics (Phase 4)    |

Two spec-level modifiers compose with any of the three:

- `Resample ∈ {jackknife, bootstrap}` — replaces analytical SE / p-values with resample-based estimates. Forces buffered.
- `Selection ∈ {forward, backward, stepwise}` — drives AIC- or BIC-based greedy subset search. Requires `Criterion`. Forces buffered.

One upstream feature operator (`FEAT_POLY`) extends the linear core to polynomial regression. Per-row attributes (`ATTR_REG_FITTED`, `ATTR_REG_RESIDUAL`, `ATTR_REG_LEVERAGE`) attach per-record diagnostics in the output row stream.

## The 13 textbook names → Pulse specs

The Indeed regression taxonomy double-counts (Simple ≡ Linear univariate, Multiple ≡ Multiple Linear) and treats orthogonal wrappers (Jackknife, Stepwise) as families. Pulse does not. The table below maps each textbook name onto the corresponding Pulse spec and links to a runnable example file under `examples/regression/`.

| #  | Indeed name        | Pulse expression                                                       | Example                                       |
|----|--------------------|------------------------------------------------------------------------|-----------------------------------------------|
| 1  | Simple             | `REG_OLS` with one predictor                                           | `examples/regression/02_simple_linear.json`   |
| 2  | Multiple           | `REG_OLS` with multiple predictors                                     | `examples/regression/03_multiple_linear.json` |
| 3  | Linear             | = #1                                                                   | `examples/regression/02_simple_linear.json`   |
| 4  | Multiple Linear    | = #2                                                                   | `examples/regression/03_multiple_linear.json` |
| 5  | Logistic           | `REG_GLM{Family:"binomial", Link:"logit"}`                             | `examples/regression/04_logistic.json`        |
| 6  | Ridge              | `REG_OLS{Penalty:"l2", Alpha:λ}`                                       | `examples/regression/05_ridge.json`           |
| 7  | Lasso              | `REG_OLS{Penalty:"l1", Alpha:λ}`                                       | `examples/regression/06_lasso.json`           |
| 8  | Polynomial         | `FEAT_POLY{Field:x, Degree:n}` upstream → `REG_OLS`                    | `examples/regression/07_polynomial.json`      |
| 9  | Bayesian Linear    | `REG_BAYES_LINEAR{Prior:"nig"}`                                        | `examples/regression/08_bayesian_linear.json` |
| 10 | Jackknife          | any regression with `Resample:"jackknife"`                             | `examples/regression/09_jackknife.json`       |
| 11 | Elastic Net        | `REG_OLS{Penalty:"elasticnet", Alpha, L1Ratio}`                        | `examples/regression/10_elasticnet.json`      |
| 12 | Ecological         | `GROUP_*` upstream → `REG_OLS` over group means (composed request)     | `examples/regression/01_ecological_fallacy.json` |
| 13 | Stepwise           | any regression with `Selection:"stepwise", Criterion:"aic"\|"bic"`     | `examples/regression/11_stepwise.json`        |

## Streamability matrix

| Spec                                              | Streamable | Memory  | Notes                                                          |
|---------------------------------------------------|------------|---------|----------------------------------------------------------------|
| `REG_OLS` no penalty                              | yes        | O(p²)   | sufficient stats: n, Σx, Σy, XᵀX, Xᵀy, Σy²                     |
| `REG_OLS` + l1 / l2 / elasticnet                  | yes        | O(p²)   | streaming Gram; regularized solve at finalize                   |
| `REG_BAYES_LINEAR` (conjugate NIG)                | yes        | O(p²)   | streaming sufficient stats + closed-form posterior update       |
| `REG_GLM` (binomial / poisson / gamma)            | no         | O(n·p)  | IRLS / Newton requires multiple passes                          |
| Any regression with `Resample != ""`              | no         | O(n·p)  | LOO / bootstrap refit                                           |
| Any regression with `Selection != ""`             | no         | O(n·p)  | refit per candidate subset                                      |

`pulse_predict` reports per-request streamability on `PredictResult.Streamable`, mirroring the runtime gate.

## Operator reference

### `REG_OLS`

Ordinary least squares with optional regularization.

| Param         | Required | Notes                                                                                  |
|---------------|----------|----------------------------------------------------------------------------------------|
| `target`      | yes      | Numeric response field.                                                                |
| `predictors`  | yes      | One or more numeric predictor fields.                                                  |
| `penalty`     | no       | `""` (default), `"l1"`, `"l2"`, or `"elasticnet"`.                                     |
| `alpha`       | conditional | Required and `> 0` when `penalty != ""`.                                            |
| `l1_ratio`    | conditional | Required and in `[0, 1]` when `penalty == "elasticnet"`.                            |
| `max_iters`   | no       | Coordinate-descent cap (default 1000).                                                 |
| `tol`         | no       | Convergence tolerance (default `1e-6`).                                                |
| `resample`    | no       | `"jackknife"` or `"bootstrap"`. Downgrades streaming.                                  |
| `selection`   | no       | `"forward"`, `"backward"`, or `"stepwise"`. Requires `criterion`. Downgrades streaming. |

Modifier compatibility: `Resample` and `Selection` may be combined; `Selection` runs first, `Resample` re-fits the selected subset.

Error codes: `PROCESSING_REGRESSION_RANK_DEFICIENT`, `PROCESSING_REGRESSION_SINGULAR_GRAM`, `PROCESSING_REGRESSION_NO_CONVERGE`, `PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `PROCESSING_REGRESSION_APPROXIMATE_SE` (warning, l1/elasticnet without resample), `PROCESSING_REGRESSION_REGULARIZED_SELECTION` (warning, penalty + selection), `PROCESSING_CONFIG`.

### `REG_GLM`

Generalized linear model via iteratively-reweighted least squares.

| Param         | Required | Notes                                                                                  |
|---------------|----------|----------------------------------------------------------------------------------------|
| `target`      | yes      | Numeric response.                                                                      |
| `predictors`  | yes      | One or more numeric predictor fields.                                                  |
| `family`      | yes      | `"binomial"`, `"poisson"`, or `"gamma"`.                                               |
| `link`        | no       | Family-specific default when empty (`binomial`→`logit`, `poisson`→`log`, `gamma`→`inverse`). |
| `max_iters`   | no       | IRLS iteration cap (default 50).                                                       |
| `tol`         | no       | Convergence tolerance (default `1e-8`).                                                |
| `resample`    | no       | `"jackknife"` or `"bootstrap"`.                                                        |
| `selection`   | no       | Subset-selection wrapper; requires `criterion`.                                        |

Always buffered. Setting `penalty` / `alpha` / `l1_ratio` on a `REG_GLM` spec is rejected with `PROCESSING_CONFIG`; regularized GLM is reserved for a later phase.

Error codes: `PROCESSING_REGRESSION_INVALID_FAMILY`, `PROCESSING_REGRESSION_INVALID_LINK`, `PROCESSING_REGRESSION_NO_CONVERGE`, `PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `PROCESSING_CONFIG`.

### `REG_BAYES_LINEAR`

Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior.

| Param             | Required | Notes                                                                              |
|-------------------|----------|------------------------------------------------------------------------------------|
| `target`          | yes      | Numeric response.                                                                  |
| `predictors`      | yes      | One or more numeric predictor fields.                                              |
| `prior`           | no       | Only `"nig"` accepted in v1. Default `"nig"`.                                      |
| `prior_mu`        | no       | Length `p+1` mean vector (intercept first); defaults to zero.                      |
| `prior_precision` | no       | Scalar `ε ≥ 0` on the precision matrix `ε·I`. Default `1e-3`.                      |
| `prior_shape`     | no       | Inverse-gamma shape `a₀`. Default `1e-3`.                                          |
| `prior_rate`      | no       | Inverse-gamma rate `b₀`. Default `1e-3`.                                           |
| `credible_level`  | no       | Posterior interval mass. Default 0.95.                                             |

Modifier compatibility: `Resample` and `Selection` are **rejected** for `REG_BAYES_LINEAR` at spec validation — the posterior already conveys uncertainty via credible intervals, and stepwise feature selection on a Bayesian model is a posterior-based question the conjugate-NIG engine doesn't support.

Setting `penalty` / `alpha` / `l1_ratio` / `family` / `link` on a Bayes spec is rejected with `PROCESSING_CONFIG`.

Error codes: `PROCESSING_REGRESSION_RANK_DEFICIENT`, `PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `PROCESSING_CONFIG`.

## Modifiers

### `Resample`

Layered on top of any base operator (except `REG_BAYES_LINEAR`).

| Value          | Behavior                                                                                                |
|----------------|---------------------------------------------------------------------------------------------------------|
| `""`           | No resampling. Closed-form / asymptotic standard errors.                                                |
| `"jackknife"`  | Leave-one-out resampling. `SE = sqrt((n−1)/n · Σᵢ (β⁽⁻ⁱ⁾ − β̄)²)`.                                       |
| `"bootstrap"`  | Non-parametric bootstrap. `bootstrap_iters` (default 1000), `rng_seed` (`0` → time-seeded; non-zero → reproducible). |

For l1 / elasticnet OLS, setting `Resample` is the rigorous answer for standard errors: it suppresses the `PROCESSING_REGRESSION_APPROXIMATE_SE` warning (the SEs are now resample-based, not plug-in over the active set).

### `Selection`

Layered on top of any base operator (except `REG_BAYES_LINEAR`).

| Value         | Behavior                                                                                            |
|---------------|-----------------------------------------------------------------------------------------------------|
| `""`          | No subset selection.                                                                                |
| `"forward"`   | Start from intercept-only; add the predictor that lowers the criterion most.                        |
| `"backward"`  | Start from full model; remove the predictor whose absence lowers the criterion most.                |
| `"stepwise"`  | Bidirectional sweep; try every add and every remove per cycle.                                      |

Requires `Criterion ∈ {"aic", "bic"}`.

- **AIC** = `-2·logL + 2·k`. Lighter penalty; may retain weak predictors at moderate `n`.
- **BIC** = `-2·logL + log(n)·k`. Heavier per-parameter penalty; rejects noise predictors more reliably at moderate `n`.

`SelectedFeatures` lists the chosen subset; `Coefficients` drops non-selected predictors entirely (absence ≠ zero — selection's contract is stronger). Selection may be combined with Resample: Selection picks the active subset, then Resample replaces SE / p-values on the selected model.

## Compositional patterns

### Polynomial regression — `FEAT_POLY` + `REG_OLS`

Polynomial regression is linear in the coefficients; the non-linearity lives in the feature space. Use `FEAT_POLY` upstream to materialize `x_2`, `x_3`, …, `x_<degree>` derived columns, then list them alongside the original `x` in `predictors`:

```json
{
  "features": [
    {"type": "FEAT_POLY", "field": "x", "label": "x", "params": {"degree": 3}}
  ],
  "regressions": [
    {"type": "REG_OLS", "name": "polyfit", "target": "y",
     "predictors": ["x", "x_2", "x_3"]}
  ]
}
```

Degree is gated at `[2, 10]`. Numerical stability is the caller's responsibility: `x^10` overflows `f64` once `|x|` clears a few hundred, and the Gram matrix conditions poorly long before that. Centre or standardize predictors before requesting `FEAT_POLY`.

### Ecological regression — group → regress

"Ecological regression" is a regression fit on aggregated group-level statistics — per-precinct means, per-county sums, per-region rates — rather than individual-level rows. Use `pulse_compose` with two slots: slot 1 produces per-group means via `GROUP_*` + `AGG_AVERAGE`, slot 2 fits `REG_OLS` over the aggregate output (or, in practice, over a pre-aggregated `.pulse` file).

The two slots are intentionally independent; Pulse does not pipe slot-1 results into slot-2 as cohort input. Either (a) materialize slot 1's aggregate as its own `.pulse` cohort upstream, or (b) treat slot 1 as the audit trail (per-group means visible in the composed response) and run slot 2 over a pre-aggregated fixture.

> **Caution — the ecological fallacy.** A significant group-level slope does **not** imply an individual-level association. Robinson (1950) showed that ecological correlations and individual correlations can take opposite signs in the same data: a per-state regression of literacy on race might suggest a strong relationship that vanishes (or reverses) at the per-person level. Aggregation collapses within-group variation, leaving only between-group structure that frequently encodes confounders.
>
> When ecological regression is the right tool: aggregate-only data (census output, public-health summary tables); genuinely group-level research questions ("do counties with higher median income have higher turnout?"). When it is the wrong tool: individual-level claims; causal claims. Annotate consumer-facing prose with this caveat; Pulse cannot enforce it.
>
> Robinson, W.S. (1950). "Ecological Correlations and the Behavior of Individuals." *American Sociological Review* 15(3): 351–357.

## Per-row regression attributes

Three attribute operators emit per-record diagnostics from a fitted regression onto the row stream.

| Attribute             | Emits per row                                                              |
|-----------------------|-----------------------------------------------------------------------------|
| `ATTR_REG_FITTED`     | `ŷ_i = Xᵢ β` — the model's prediction at each row.                          |
| `ATTR_REG_RESIDUAL`   | `y_i − ŷ_i` — the per-row residual.                                         |
| `ATTR_REG_LEVERAGE`   | `h_ii = Xᵢ (XᵀX)⁻¹ Xᵢᵀ` — the i-th diagonal of the hat matrix.              |

Each attribute references a sibling regression spec by `regression_name`. See `skills/attribute-composition.md` for the parameter table.

## Error codes

Look up full prose via `pulse_errors_lookup` or `pulse errors lookup CODE`.

| Code                                          | Meaning (one-liner)                                                           |
|-----------------------------------------------|-------------------------------------------------------------------------------|
| `PROCESSING_REGRESSION_NOT_IMPLEMENTED`       | Reserved as of Phase 8; no engine returns this today.                         |
| `PROCESSING_REGRESSION_RANK_DEFICIENT`        | XᵀX is singular; add regularization or drop a predictor.                       |
| `PROCESSING_REGRESSION_NO_CONVERGE`           | IRLS or coordinate descent failed within `MaxIters`.                          |
| `PROCESSING_REGRESSION_SINGULAR_GRAM`         | XᵀX non-invertible even after regularization; increase `alpha`.               |
| `PROCESSING_REGRESSION_INVALID_FAMILY`        | `REG_GLM` Family outside `{binomial, poisson, gamma}`.                        |
| `PROCESSING_REGRESSION_INVALID_LINK`          | Link incompatible with the chosen Family.                                     |
| `PROCESSING_REGRESSION_INSUFFICIENT_DATA`     | Filtered set has fewer rows than predictors + 1, or below resample minimum.   |
| `PROCESSING_REGRESSION_APPROXIMATE_SE`        | Warning: l1 / elasticnet SE is a plug-in approximation; set `resample` for rigor. |
| `PROCESSING_REGRESSION_REGULARIZED_SELECTION` | Warning: `penalty != ""` plus `selection != ""` is unusual.                   |
| `PROCESSING_CONFIG`                           | Invalid spec combination (e.g. Bayes + Resample, GLM + Penalty).              |

## Worked examples

Every Indeed name has a runnable JSON file under `examples/regression/`. Fetch via `pulse_examples_get` or read directly:

- [01_ecological_fallacy.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/01_ecological_fallacy.json) — per-region aggregation + ecological caveat (#12).
- [02_simple_linear.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/02_simple_linear.json) — univariate OLS (#1, #3).
- [03_multiple_linear.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/03_multiple_linear.json) — multivariate OLS (#2, #4).
- [04_logistic.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/04_logistic.json) — binary classification (#5).
- [05_ridge.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/05_ridge.json) — l2 penalty (#6).
- [06_lasso.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/06_lasso.json) — l1 penalty (#7).
- [07_polynomial.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/07_polynomial.json) — `FEAT_POLY` + OLS (#8).
- [08_bayesian_linear.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/08_bayesian_linear.json) — conjugate NIG (#9).
- [09_jackknife.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/09_jackknife.json) — leave-one-out resampling (#10).
- [10_elasticnet.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/10_elasticnet.json) — combined l1 / l2 penalty (#11).
- [11_stepwise.json](https://github.com/frankbardon/pulse/blob/main/examples/regression/11_stepwise.json) — BIC-driven stepwise selection (#13).
