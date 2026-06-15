---
name: regression-modeling
description: REG_* operator + modifier composition (`Resample`, `Selection`), `FEAT_POLY` upstream composition, how 13 textbook regression names map to 3 operators + 2 modifiers. Topical design; per-REG detail in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [REG, REG_OLS, REG_GLM, REG_BAYES_LINEAR, Resample, Selection, FEAT_POLY, regressions, modifiers]
---

# Regression modeling

Three operators + two modifiers + one upstream feature operator cover every textbook regression name. Per-engine math (IRLS, NIG, coordinate descent) in atomic `op-reg-*` skills; this file covers composition.

Regressions do not emit `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

## Operator surface

| Operator | Streaming | Fits |
|---|---|---|
| `REG_OLS` | yes (sufficient stats) | OLS; optional `l1` / `l2` / `elasticnet` penalty |
| `REG_GLM` | no (IRLS) | GLM; `Family ∈ {binomial, poisson, gamma}` + `Link` |
| `REG_BAYES_LINEAR` | yes (Welford + posterior update) | Bayesian linear, conjugate Normal-Inverse-Gamma prior |

`REG_OLS` + `REG_BAYES_LINEAR` share the Welford-Pébaÿ accumulator (`n, μ_x, μ_y, M2_xx, M2_xy, M2_yy`); `REG_BAYES_LINEAR` recovers `XᵀX`, `Xᵀy`, `yᵀy` and adds one finalize-time Cholesky on `Λ_n`. `REG_GLM` buffers and iterates.

Target / predictor types follow `encoding.FieldType.IsNumericForAnalytics`: integer / float / decimal plus `u4`, `packed_bool`, `date`. Nullable fields drop the row from the observation count via per-record bitmap — no formula cast needed.

## Modifiers

Both modifiers force buffered execution. Both compose with `REG_OLS` and `REG_GLM`. `REG_BAYES_LINEAR` rejects both — credible intervals already convey uncertainty, stepwise on a NIG fit is out of scope.

### `Resample` (Indeed #10 Jackknife)

| Value | Behavior |
|---|---|
| `""` | closed-form / asymptotic SE |
| `"jackknife"` | LOO refit; SE = √((n−1)/n · Σ(β⁽⁻ⁱ⁾ − β̄)²) |
| `"bootstrap"` | non-parametric (`bootstrap_iters`, `rng_seed`); SE = sample std of replicates; percentile-method p |

Overwrites `StdErrors` / `PValues`; point estimate stays at the full-data fit. `BootstrapIters` defaults to 1000; `RNGSeed = 0` time-seeds, non-zero reproducible. For `l1` / `elasticnet`, `Resample` is the rigorous SE answer and suppresses `PROCESSING_REGRESSION_APPROXIMATE_SE`.

### `Selection` (Indeed #13 Stepwise)

| Value | Behavior |
|---|---|
| `""` | fit on all predictors |
| `"forward"` | start intercept-only; add the predictor that lowers `Criterion` most |
| `"backward"` | start full; remove the predictor whose absence lowers `Criterion` most |
| `"stepwise"` | bidirectional sweep |

`Criterion ∈ {aic, bic}` required. BIC rejects noise predictors more reliably at moderate `n`. `SelectedFeatures` lists chosen predictors; non-selected ones are DROPPED from `Coefficients` — absent ≠ zero.

`Selection` + `Resample` together is sane. `Penalty != ""` + `Selection != ""` emits `PROCESSING_REGRESSION_REGULARIZED_SELECTION` — regularization already shrinks / selects.

## `FEAT_POLY` upstream (Polynomial)

`FEAT_POLY` runs in `features` before `REG_OLS`, emitting `Degree − 1` derived columns (`<label>_2 … <label>_<Degree>`). The original column stays. Degree gate `[2, 10]`. Standardize predictors — `x^10` overflows `f64` past `|x| ≈ a few hundred`. Detail: `feature-engineering`.

## 13 textbook names → spec

| # | Name | Spec |
|---|---|---|
| 1, 3 | Simple / Linear | `REG_OLS` one predictor |
| 2, 4 | Multiple / Multiple Linear | `REG_OLS` multiple predictors |
| 5 | Logistic | `REG_GLM{Family:"binomial", Link:"logit"}` |
| 6 | Ridge | `REG_OLS{Penalty:"l2", Alpha:λ}` |
| 7 | Lasso | `REG_OLS{Penalty:"l1", Alpha:λ}` |
| 8 | Polynomial | `FEAT_POLY` upstream → `REG_OLS` |
| 9 | Bayesian Linear | `REG_BAYES_LINEAR{Prior:"nig"}` |
| 10 | Jackknife | any with `Resample:"jackknife"` |
| 11 | Elastic Net | `REG_OLS{Penalty:"elasticnet", Alpha, L1Ratio}` |
| 12 | Ecological | `GROUP_*` + `AGG_AVERAGE` upstream → `REG_OLS` over per-group means (composed) |
| 13 | Stepwise | any with `Selection:"stepwise", Criterion:"aic"\|"bic"` |

Runnable JSON: `examples/regression/`.

## Ecological caveat

A significant group-level slope does NOT imply individual-level association (Robinson 1950, Simpson). Use ecological fits only when the question is about groups or individual data is unavailable (census, precincts). Pulse cannot enforce this — annotate consumer prose.

## Inference

- `REG_OLS` (unpenalized, `l2`): asymptotic SE from `(XᵀX)⁻¹`. Student-t p.
- `REG_OLS` (`l1`, `elasticnet`): plug-in SE over data-dependent active set; shrunk-to-zero coefficients have no SE. `PROCESSING_REGRESSION_APPROXIMATE_SE` warning unless `Resample` is set.
- `REG_GLM`: Wald-z p from `Cov(β) = (XᵀWX)⁻¹`. Dispersion=1 for `binomial`/`poisson`; gamma same assumption (skeptical).
- `REG_BAYES_LINEAR`: posterior means + credible intervals from Student-t marginal. `p_values` NOT emitted.

## Gotchas

- `REG_GLM` always buffered. Modifiers also force buffered.
- `Penalty != ""` on `REG_GLM` rejected (`PROCESSING_CONFIG`) — regularized GLM is a later phase.
- `Penalty` / `Alpha` / `L1Ratio` / `Family` / `Link` on `REG_BAYES_LINEAR` rejected — those knobs belong to other engines.
- Nullables drop rows from `n_obs`; counts ≠ raw record count when nullable fields appear.
- `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`: drop a predictor or add regularization.
- `PROCESSING_REGRESSION_NO_CONVERGE`: raise `MaxIters` / `Tol`, or reduce `Alpha`.

## See

- Recipes: `pulse_examples_search tags=["regression"]` plus atomic `op-reg-<name>`.
- `feature-engineering` — `FEAT_POLY` parameter table + column naming.
- `statistical-testing` — Wald-z vs Student-t.
- `request-envelope` — slot keys, streamability.
- `error-code-reference` — `PROCESSING_REGRESSION_*` recovery.
