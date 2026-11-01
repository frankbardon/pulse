# Regression Modeling

Pulse exposes regression through a compact, composable surface. A small set of `REG_*` operators (`pulse manifest --json` lists them under `regressions`), two orthogonal modifiers, and one upstream feature transform together cover every textbook regression variant. This chapter is the human-facing counterpart to `skills/regression-modeling.md`; agents should fetch the skill via `pulse_skills_get` rather than read this page.

## Overview

| Operator           | Engine                                       | Streaming                                  |
|--------------------|----------------------------------------------|--------------------------------------------|
| `REG_OLS`          | Ordinary least squares + optional regularization | Streams sufficient statistics (Phase 1 + 2) |
| `REG_GLM`          | Generalized linear model via IRLS            | Always buffered (Newton-Raphson refit)     |
| `REG_BAYES_LINEAR` | Bayesian linear regression (conjugate NIG)   | Streams sufficient statistics (Phase 4)    |
| `REG_MMM_HILL`     | Media-mix model: joint nonlinear least squares over Hill-saturated, already-adstocked channels plus linear controls | Always buffered |
| `REG_MMM_BAYES`    | Bayesian media-mix model: raw spend adstocked in the model, Hill saturation, linear controls, posterior sampled by NUTS | Always buffered |

Two spec-level modifiers compose with the first three (`REG_MMM_HILL` takes `Resample` only; its own knobs ride `params`, and it names its own curve coefficients `k:<channel>` / `s:<channel>` — skill `op-reg-mmm-hill`, example `internal/examples/regression/19_mmm_hill.json`). `REG_MMM_BAYES` takes neither: its sampler, priors and model-owned holdout (`holdout_from`) ride `params`, it has no scorer (no `save`, engine `holdout` or `ATTR_REG_PREDICT`), and it names `decay:<channel>`, `k:<channel>`, `s:<channel>` and `sigma` beside the predictors, its ROI / mROI / contribution intervals and sampler diagnostics riding `outputs` — skill `op-reg-mmm-bayes`, example `internal/examples/regression/20_mmm_bayes.json`, design guide `mmm-workflow`. The modifiers:

- `Resample ∈ {jackknife, bootstrap}` — replaces analytical SE / p-values with resample-based estimates. Forces buffered.
- `Selection ∈ {forward, backward, stepwise}` — drives AIC- or BIC-based greedy subset search. Requires `Criterion`. Forces buffered.

One upstream feature operator (`FEAT_POLY`) extends the linear core to polynomial regression. Per-row attributes (`ATTR_REG_FITTED`, `ATTR_REG_RESIDUAL`, `ATTR_REG_LEVERAGE`, `ATTR_REG_INFLUENCE`) attach per-record diagnostics in the output row stream.

## The 13 textbook names → Pulse specs

The Indeed regression taxonomy double-counts (Simple ≡ Linear univariate, Multiple ≡ Multiple Linear) and treats orthogonal wrappers (Jackknife, Stepwise) as families. Pulse does not. The table below maps each textbook name onto the corresponding Pulse spec and links to a runnable example file under `internal/examples/regression/`.

| #  | Indeed name        | Pulse expression                                                       | Example                                       |
|----|--------------------|------------------------------------------------------------------------|-----------------------------------------------|
| 1  | Simple             | `REG_OLS` with one predictor                                           | `internal/examples/regression/02_simple_linear.json`   |
| 2  | Multiple           | `REG_OLS` with multiple predictors                                     | `internal/examples/regression/03_multiple_linear.json` |
| 3  | Linear             | = #1                                                                   | `internal/examples/regression/02_simple_linear.json`   |
| 4  | Multiple Linear    | = #2                                                                   | `internal/examples/regression/03_multiple_linear.json` |
| 5  | Logistic           | `REG_GLM{Family:"binomial", Link:"logit"}`                             | `internal/examples/regression/04_logistic.json`        |
| 6  | Ridge              | `REG_OLS{Penalty:"l2", Alpha:λ}`                                       | `internal/examples/regression/05_ridge.json`           |
| 7  | Lasso              | `REG_OLS{Penalty:"l1", Alpha:λ}`                                       | `internal/examples/regression/06_lasso.json`           |
| 8  | Polynomial         | `FEAT_POLY{Field:x, Degree:n}` upstream → `REG_OLS`                    | `internal/examples/regression/07_polynomial.json`      |
| 9  | Bayesian Linear    | `REG_BAYES_LINEAR{Prior:"nig"}`                                        | `internal/examples/regression/08_bayesian_linear.json` |
| 10 | Jackknife          | any regression with `Resample:"jackknife"`                             | `internal/examples/regression/09_jackknife.json`       |
| 11 | Elastic Net        | `REG_OLS{Penalty:"elasticnet", Alpha, L1Ratio}`                        | `internal/examples/regression/10_elasticnet.json`      |
| 12 | Ecological         | `GROUP_*` upstream → `REG_OLS` over group means (composed request)     | `internal/examples/regression/01_ecological_fallacy.json` |
| 13 | Stepwise           | any regression with `Selection:"stepwise", Criterion:"aic"\|"bic"`     | `internal/examples/regression/11_stepwise.json`        |

## Streamability matrix

| Spec                                              | Streamable | Memory  | Notes                                                          |
|---------------------------------------------------|------------|---------|----------------------------------------------------------------|
| `REG_OLS` no penalty                              | yes        | O(p²)   | sufficient stats: n, Σx, Σy, XᵀX, Xᵀy, Σy²                     |
| `REG_OLS` + l1 / l2 / elasticnet                  | yes        | O(p²)   | streaming Gram; regularized solve at finalize                   |
| `REG_BAYES_LINEAR` (conjugate NIG)                | yes        | O(p²)   | streaming sufficient stats + closed-form posterior update       |
| `REG_GLM` (binomial / poisson / gamma / NB)       | no         | O(n·p)  | IRLS / Newton requires multiple passes                          |
| Any regression with `Resample != ""`              | no         | O(n·p)  | LOO / bootstrap refit                                           |
| Any regression with `Selection != ""`             | no         | O(n·p)  | refit per candidate subset                                      |
| `REG_OLS` / `REG_BAYES_LINEAR` with a robust `covariance` (not `classical`) | no | O(n·p) | second pass for residuals / leverage                |

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
| `l1_ratio`    | conditional | Required and in `(0, 1)` when `penalty == "elasticnet"`.                            |
| `max_iters`   | no       | Coordinate-descent cap (default 1000).                                                 |
| `tol`         | no       | Convergence tolerance (default `1e-6`).                                                |
| `resample`    | no       | `"jackknife"` or `"bootstrap"`. Downgrades streaming.                                  |
| `jackknife_groups` / `jackknife_by` | no | Delete-a-group jackknife (see `Resample` below); exclusive. |
| `selection`   | no       | `"forward"`, `"backward"`, or `"stepwise"`. Requires `criterion`. Downgrades streaming. |
| `conf_level`  | no       | Coverage of `ci_lower` / `ci_upper`, `0 < c < 1`. Default 0.95. Student t on the residual df. |
| `bounds`      | no       | `{nonnegative: [predictors]}`: those slopes are constrained `>= 0` (non-negative least squares); see "Non-negative slopes" below. |
| `intercept`   | no       | `true` (default) fits the intercept. `false` fits through the origin; see "Solver and intercept" below. |
| `solver`      | no       | `qr` (default) or `normal`; see "Solver and intercept" below. |
| `covariance`  | no       | `{type, cluster, order_by, lags}`: `classical` (default), `hc0`–`hc3`, `cluster` (CR1 on `cluster`'s levels, df = clusters − 1) or `hac` (Newey–West over `order_by`, `lags` an integer or `"auto"`). Drives `std_errors`, `p_values`, the intervals and `vcov`; refused beside a penalty, `resample` or `selection` (`PROCESSING_REGRESSION_VCOV_UNSUPPORTED`). Downgrades streaming. |

Modifier compatibility: `Resample` and `Selection` may be combined; `Selection` runs first, `Resample` re-fits the selected subset.

Unpenalized, `coefficients`, `std_errors`, `p_values`, `ci_lower` / `ci_upper`, `r2`, `adj_r2`, `residual_std_err` and `df` are R `lm()`'s (`summary`, `confint`) to relative `1e-10`; under frequency weights they are `lm()` on the rows repeated `w` times. Oracle: `scripts/reference/gen_ols_lm.R`.

Every penalty minimizes one objective, glmnet's with `standardize = FALSE` and the same one `REG_GLM` uses: `(1/2N*)·Σw·(y − β₀ − Xβ)² + alpha·[(1 − l1_ratio)/2·‖β‖² + l1_ratio·‖β‖₁]` (`l2` is `l1_ratio = 0`, `l1` is `1`), `N*` the sum of the weights, the intercept unpenalized and the predictors on their own units — so `l2` at `alpha` is the elastic net's limit at `l1_ratio → 0` at the same `alpha`, and a predictor on a large scale is barely shrunk (divide it by its SD upstream to penalize all alike). `l2` is solved in closed form, `l1` / `elasticnet` by coordinate descent on the centred Gram. `l2` standard errors are the closed-form ridge sandwich σ̂²·A⁻¹·G·A⁻¹ (`A = G + N*·alpha·I`, `G` the centred Gram, σ̂² = RSS / (N* − p − 1)) with Student-t p-values on N* − p − 1 df; `l1` / `elasticnet` report `std_errors`, `p_values` and intervals `null` on every coefficient (the data pick the predictors, so no analytical inference is valid) with the advisory `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`. `unpenalized` (`l2` only) lists predictors the ridge term skips — glmnet's `penalty.factor` 0, `A = G + N*·alpha·diag(pf)` in the sandwich — for controls or dummies that should stay unshrunk; `l1` / `elasticnet` (and no penalty) refuse it with `PROCESSING_CONFIG`. Oracle: R `glmnet` for the coefficients (with `penalty.factor` for `unpenalized`) and R matrix algebra for the ridge covariance (`scripts/reference/gen_ols_penalty.R`).

**Non-negative slopes.** `bounds.nonnegative` lists predictors whose coefficient must be `>= 0`; the intercept and every other slope stay free. Pulse solves the bounded least-squares problem by Lawson–Hanson's active-set method on the centred Gram (so it streams and takes weights). When the unconstrained fit already satisfies every bound, the result is that fit byte for byte. When a bound binds — a coefficient is held at exactly `0` — the estimator is constrained and its classical inference is not valid: `coefficients`, `r2`, `adj_r2` and `residual_std_err` stand, while `std_errors`, `p_values`, `ci_lower` / `ci_upper` and every `vcov` entry are `null`, and the result carries the advisory `PULSE_ADVISORY_CONSTRAINT_ACTIVE` (`details.active` names the held predictors). It is result-only: predict cannot know whether a bound will bind. For intervals of the bounded fit set `resample: bootstrap` — each replicate refits the bounded model. `bounds` is refused `PROCESSING_CONFIG` with any `penalty` or `selection`, on `REG_GLM` / `REG_BAYES_LINEAR`, or for an unknown or repeated name; a robust `covariance` is `PROCESSING_REGRESSION_VCOV_UNSUPPORTED` (`reason: bounds`). Oracle: R `nnls` (`scripts/reference/gen_ols_nnls.R`), including a near-collinear case.

**Solver and intercept.**

- **`solver: "qr"` (the default).** An unpenalized, unbounded fit solves a streaming QR factorization of the weighted design (Givens rotations, as in R's `biglm`). The design is shifted to its first row, so a predictor with a large offset stays well separated from the intercept column. The solve works at κ(X), so ill-conditioned designs keep their digits: NIST Filip fits to 7.4 digits, and Wampler1–5 reach 5.7–13.3 (see "How Pulse is validated").
- **`solver: "normal"`.** Solves the normal equations by Cholesky on the centred Gram matrix, at κ(X)², the method used before v1.0.0-alpha.35. Use it to reproduce an older result bit for bit, or to match a package that solves the normal equations. Both solvers stream.
- **`intercept: false`.** Fits through the origin. The intercept is pinned to 0, not estimated: `coefficients["(intercept)"]` is `0`, so a bound fit scores unchanged, and its `std_errors`, `p_values`, `ci_lower` / `ci_upper` and `vcov` row are `null`. As R's `lm(y ~ 0 + x)` reports, `r2` is the uncentred Σŷ² / Σy², the residual `df` is N* − p, and `adj_r2` is 1 − (1 − R²)·N*/df. NIST NoInt1 and NoInt2 agree to 14 and 15 digits.
- **Refusals.** Both knobs are `REG_OLS` only and are refused `PROCESSING_CONFIG` (predict too) on another type. `solver` is also refused beside a penalty or `bounds`. `intercept: false` is also refused beside a penalty, `bounds`, `selection`, `resample`, `holdout`, a robust `covariance` or `solver: "normal"`.

Error codes: `PROCESSING_REGRESSION_RANK_DEFICIENT`, `PROCESSING_REGRESSION_SINGULAR_GRAM`, `PROCESSING_REGRESSION_NO_CONVERGE`, `PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` (advisory, l1/elasticnet without resample: null SEs / p-values), `PULSE_ADVISORY_CONSTRAINT_ACTIVE` (advisory, a binding `bounds` without resample: null SEs / p-values), `PROCESSING_REGRESSION_REGULARIZED_SELECTION` (warning, penalty + selection), `PROCESSING_CONFIG`.

### `REG_GLM`

Generalized linear model via iteratively-reweighted least squares.

| Param         | Required | Notes                                                                                  |
|---------------|----------|----------------------------------------------------------------------------------------|
| `target`      | yes      | Numeric response.                                                                      |
| `predictors`  | yes      | One or more numeric predictor fields.                                                  |
| `family`      | yes      | `"binomial"`, `"poisson"`, `"gamma"`, or `"negative_binomial"` (NB2, below).                       |
| `link`        | no       | One of the family's links, the canonical one the default when empty: `binomial` `logit` / `probit` / `cloglog`, `poisson` `log` / `identity` / `sqrt`, `gamma` `inverse` / `log` / `identity`, `negative_binomial` `log` / `identity` / `sqrt`. Another family's link is `PROCESSING_REGRESSION_INVALID_LINK` (predict too). |
| `theta`       | no       | `negative_binomial` only: a fixed θ > 0 (R `glm(family = negative.binomial(theta))`); absent estimates θ. Elsewhere, or ≤ 0, `PROCESSING_CONFIG`. |
| `max_iters`   | no       | IRLS iteration cap (default 50).                                                       |
| `tol`         | no       | Convergence tolerance (default `1e-8`).                                                |
| `penalty`     | no       | `"l2"`, `"l1"`, `"elasticnet"` (binomial / poisson only; gamma and negative_binomial refuse it). |
| `alpha`       | no       | Penalty strength, `> 0` when `penalty` is set.                                         |
| `l1_ratio`    | no       | Elastic-net mix, `0 < l1_ratio < 1`; `elasticnet` only.                                |
| `resample`    | no       | `"jackknife"` or `"bootstrap"`; `jackknife_groups` / `jackknife_by` as for REG_OLS.         |
| `selection`   | no       | Subset-selection wrapper; requires `criterion`.                                        |
| `conf_level`  | no       | Coverage of `ci_lower` / `ci_upper` (Wald z; gamma t on `df`), `0 < c < 1`. Default 0.95. |
| `covariance`  | no       | As `REG_OLS`: the sandwich over the IRLS scores at the converged weights (R `sandwich` on `glm`) behind `std_errors`, `p_values`, the intervals and `vcov`. Dispersion-free; inference stays Wald z, `cluster` and gamma included. |

Always buffered.

**Dispersion.** Binomial and poisson fix the dispersion at 1 (the family assumption): standard errors from `(XᵀWX)⁻¹`, Wald z p-values and intervals, no `df`. Gamma estimates it, as R's `summary.glm` does: φ̂ = Pearson χ² / `df`, `df` = N* − p − 1 (N* = n unweighted, Σw under frequency weights, Kish n_eff under probability weights), standard errors and `vcov` scaled by φ̂, p-values and intervals on a Student t with `df`, which the result reports (`null`, with every standard error, p-value and bound, when it is 0 or below). φ̂ itself is not emitted. Oracle: R `glm(family = Gamma())` (`scripts/reference/gen_glm_gamma.R`). A gamma `selection` scores each candidate by R's `AIC(glm)` / `BIC(glm)` — the gamma log-likelihood at dispersion deviance / n, the dispersion counted as a parameter — not the deviance + 2k that binomial and poisson use (`scripts/reference/gen_glm_gamma_selection.R`).

**Penalties.** `penalty` minimizes glmnet's objective with `standardize = FALSE` — `−loglik/N* + alpha·[(1 − l1_ratio)/2·‖β‖² + l1_ratio·‖β‖₁]`, `N*` the sum of the (prior) weights, the predictors on their own units and the intercept unpenalized (`l2` is `l1_ratio = 0`, `l1` is `1`). Each IRLS step solves the penalized working least-squares problem: in closed form for `l2`, by coordinate descent with an exact active-set finish for `l1` / `elasticnet`. For `l2`, standard errors, Wald p-values and intervals read the penalized-Fisher sandwich `H⁻¹·XᵀWX·H⁻¹`, `H = XᵀWX + N*·alpha·D` (`D` zero on the intercept). For `l1` / `elasticnet` they are `null` on every coefficient, the intercept included — the data choose the active set, so no analytical sampling distribution exists — and the fit (and predict) carries `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` unless `resample` is set; `vcov` is refused `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. `deviance` and `pseudo_r2` are the unpenalized figures at the penalized estimate. A penalty rides `resample` (every replicate penalized) and `selection` (predict warns `PROCESSING_REGRESSION_REGULARIZED_SELECTION`). An `l2` penalty keeps a separated binomial fit finite. Oracle: R `glmnet` (`scripts/reference/gen_glm_penalty.R`).

The fit is R's `glm.fit` IRLS: per-row starting values (binomial `(w·y + 0.5)/(w + 1)`, poisson `y + 0.1`, gamma `y`), the step halved back toward the previous coefficients whenever the deviance rises, and convergence on the relative change in the coefficients. An up-front QR rank check of the (weighted) design is the only source of `PROCESSING_REGRESSION_RANK_DEFICIENT` (`details.columns` names the aliased columns). A binomial fit whose fitted probabilities reach numerically 0 or 1 while a predictor's coefficient keeps climbing is separated — the maximum-likelihood estimate is at infinity — and is refused `PROCESSING_REGRESSION_SEPARATION` (`details.predictors`) rather than reporting wherever the loop stopped.

**Links.** Every link is R's (`stats` `make.link`): probit `μ = Φ(η)` and cloglog `μ = 1 − exp(−e^η)` clamp μ to [ε, 1 − ε] and floor dμ/dη at ε as R does; the poisson and gamma identity links and the poisson sqrt link keep μ positive, so a step that would push a fitted mean through 0 is halved, as `glm.fit` does. Nothing assumes a canonical link: the working weight is `(dμ/dη)²/V(μ)`, the score `x·(y − μ)·(dμ/dη)/V(μ)`, so `vcov` is φ·(XᵀWX)⁻¹ at the expected information (R's `summary.glm`), the robust sandwich reads those scores (R `sandwich`), the working residual is `(y − μ)·g′(μ)`, the leverage is `W½X(XᵀWX)⁻¹XᵀW½`, and DFFITS carries the sign of g′(μ). Off the canonical link IRLS is Fisher scoring, which converges linearly: at the default `tol` the coefficients sit within about 1e-7 (relative) of the MLE; `tol: 1e-12` reaches R's to 1e-13. A first step that lands outside the link's range (R's glm stops there, "no valid set of coefficients") is halved toward the intercept-only fit instead. Defaults are unchanged: an omitted link is the canonical one, byte for byte. Penalties, `selection`, `resample`, `covariance`, the per-row attributes over a bound fit and model files all take every link; the file's `link` key carries it. Oracle: R 4.6.1 `glm(family = <family>(link = <link>))` and `sandwich` 3.1-3 (`scripts/reference/gen_glm_links.R`).

**Negative binomial.** `family: "negative_binomial"` is NB2, Var = μ + μ²/θ, for counts more variable than a poisson allows. θ is estimated by maximum likelihood as MASS `glm.nb` does: a poisson fit on the same link, then rounds of θ ← the Newton root of the θ score at the fitted means (MASS `theta.ml`, from the moment estimate, then the previous θ) and β ← IRLS at that θ (warm-started), until a round moves θ by at most `tol` relative and β by less than `tol`; one more IRLS fit at the final θ makes β, the deviances and `vcov` those at θ̂. That is the joint MLE `glm.nb` approximates (its own stopping rule leaves it ~1e-6 off; the oracle pushes it to the fixed point). `theta` fixes θ instead: R's `glm(family = negative.binomial(theta))`. The result adds `theta`, `theta_std_err` (1/√I(θ̂), estimated θ only), `two_log_lik` and `aic` (−2ℓ + 2·(p + 1), + 2 when θ is estimated: MASS's AIC counts θ). Standard errors and `vcov` are (XᵀWX)⁻¹ at θ̂ with φ = 1, Wald z (`summary.negbin`, which treats β and θ as orthogonal — also for a fixed θ, where R's `summary.glm` would estimate a dispersion). `deviance` / `null_deviance` are at θ̂. A robust `covariance` holds θ̂ fixed (as `sandwich` on a `glm.nb` fit); the residual kinds, leverage and influence over a bound fit read V(μ) at θ̂ with φ = 1; scoring needs only the link. Weights (frequency and probability) enter as prior weights like every family; a frequency fit is the `rep()` expansion's. `penalty`, `selection` and `resample` are refused (`PROCESSING_CONFIG`, `details.requires` `unpenalized` / `no_selection` / `no_resample`: their refit drivers hold θ fixed). Counts no more variable than a poisson put θ̂ at infinity: `PROCESSING_REGRESSION_NO_CONVERGE` `details.reason` `theta_unbounded` (fit `poisson`). A model file carries θ (`model_format_version` 5). Oracle: R 4.6.1, MASS 7.3-66, `sandwich` 3.1-3 (`scripts/reference/gen_glm_nb.R`).

Error codes: `PROCESSING_REGRESSION_INVALID_FAMILY`, `PROCESSING_REGRESSION_INVALID_LINK`, `PROCESSING_REGRESSION_RANK_DEFICIENT`, `PROCESSING_REGRESSION_SEPARATION`, `PROCESSING_REGRESSION_NO_CONVERGE`, `PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `PROCESSING_CONFIG`.

### `REG_BAYES_LINEAR`

Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior.

| Param             | Required | Notes                                                                              |
|-------------------|----------|------------------------------------------------------------------------------------|
| `target`          | yes      | Numeric response.                                                                  |
| `predictors`      | yes      | One or more numeric predictor fields.                                              |
| `prior`           | no       | Only `"nig"` accepted in v1. Default `"nig"`.                                      |
| `prior_mu`        | no       | Length `p+1` mean vector (intercept first); defaults to zero.                      |
| `prior_precision` | no       | Scalar `ε ≥ 0` on the precision matrix `ε·I` (default `1e-3`), or a list of `p+1` entries, intercept first like `prior_mu`, for `Λ₀ = diag(v)`: `0` is a flat prior on that coefficient, a large entry pins it to its `prior_mu`. A wrong length or a negative entry is `PROCESSING_CONFIG` (predict too); flat entries the data cannot identify leave the posterior improper (`PROCESSING_REGRESSION_RANK_DEFICIENT`). Oracle: `scripts/reference/gen_bayes_precision.R`. |
| `prior_shape`     | no       | Inverse-gamma shape `a₀`. Default `1e-3`.                                          |
| `prior_rate`      | no       | Inverse-gamma rate `b₀`. Default `1e-3`.                                           |
| `credible_level`  | no       | Posterior interval mass. Default 0.95.                                             |
| `conf_level`      | no       | Mass of `ci_lower` / `ci_upper` (the same posterior t interval). Defaults to `credible_level`. |
| `covariance`      | no       | As `REG_OLS`. Keeps the posterior-mean coefficients but REPLACES the posterior covariance with Müller's (2013) sandwich posterior N(μ_n, Λ_n⁻¹·M·Λ_n⁻¹): `std_errors`, `credible_intervals`, `ci_lower` / `ci_upper` (normal, no `df`) and `vcov` follow it. A substitution, not the conjugate posterior. Downgrades streaming. |

Every fit with standard errors reports `ci_lower` / `ci_upper` (keyed like `coefficients`), `conf_level` and, for a t interval, `df`: OLS t on the residual df (N* − p − 1 on a weighted fit), GLM Wald normal (R's `confint.default`) for binomial / poisson / negative binomial and t on the residual df for gamma, Bayes the posterior t on 2·a_n, and any fit under `resample` normal on the resampled standard errors. An interval exists exactly where a standard error does; an undefined bound is `null`.

Modifier compatibility: `Resample` and `Selection` are **rejected** for `REG_BAYES_LINEAR` at spec validation, by predict and the runtime alike (`internal/regbayes`) — the posterior already conveys uncertainty via credible intervals, and stepwise feature selection on a Bayesian model is a posterior-based question the conjugate-NIG engine doesn't support.

Setting `penalty` / `alpha` / `l1_ratio` / `unpenalized` / `family` / `link` on a Bayes spec is rejected with `PROCESSING_CONFIG`.

Error codes: `PROCESSING_REGRESSION_RANK_DEFICIENT`, `PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `PROCESSING_CONFIG`.

## Modifiers

### `Resample`

Layered on top of any base operator (except `REG_BAYES_LINEAR`).

| Value          | Behavior                                                                                                |
|----------------|---------------------------------------------------------------------------------------------------------|
| `""`           | No resampling. Closed-form / asymptotic standard errors.                                                |
| `"jackknife"`  | Leave-one-out resampling, `SE = sqrt((n−1)/n · Σᵢ (β⁽⁻ⁱ⁾ − β̄)²)`; or delete-a-group with `jackknife_groups: g` (fitted rows split into g seeded random groups, `rng_seed`) or `jackknife_by: field` (one group per distinct value; a null value drops the row): `SE = sqrt((g−1)/g · Σₖ (β₍ₖ₎ − β̄)²)`, echoed `jackknife_groups`. The two knobs are exclusive. Weighted (JK1): each replicate refits the remaining rows with their own weights. Every replicate must refit — the first failure fails the slot with its code (`details.jackknife_group`). Predict's `PULSE_ADVISORY_JACKKNIFE_COST` flags leave-one-out at ≥ 10,000 records (Process predict, and each Compose slot and fit). Leave-one-out deletes each FITTED row: a row the fit drops (null target or predictor, zero weight) is no replicate and no part of n. |
| `"bootstrap"`  | Non-parametric bootstrap. `bootstrap_iters` (default 1000), `rng_seed` (`0` → a seed derived from the request and the rows the fit reads, so the same request over the same cohort is reproducible; non-zero → reproducible across cohorts too). |

An l1 / elasticnet fit reports `null` standard errors, p-values and intervals (advisory `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`); setting `Resample` replaces them with replicate ones and silences the advisory.

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

Four attribute operators emit per-record diagnostics from a fitted regression onto the row stream.

| Attribute             | Emits per row                                                              |
|-----------------------|-----------------------------------------------------------------------------|
| `ATTR_REG_FITTED`     | `ŷ_i = Xᵢ β` — the model's prediction at each row.                          |
| `ATTR_REG_RESIDUAL`   | `y_i − ŷ_i` — the per-row residual; `params.kind` `standardized` / `studentized` on the refit (R `rstandard` / `rstudent`). |
| `ATTR_REG_LEVERAGE`   | `h_ii = Xᵢ (XᵀX)⁻¹ Xᵢᵀ` — the i-th diagonal of the hat matrix.              |
| `ATTR_REG_INFLUENCE`  | Cook's distance (`params.measure` `cooks`, the default) or DFFITS (`dffits`), R `cooks.distance` / `dffits`. |

### Studentized residuals and influence

With `h` the leverage, `p′ = p + 1` coefficients, `df = n − p′`, `s² = RSS/df` and `e = y − ŷ`:

| Figure | Formula |
|---|---|
| standardized `r` | `e / (s·√(1 − h))` |
| studentized `t` | `r·√((df − 1)/(df − r²))` |
| Cook's distance | `r²·h / (p′·(1 − h))` |
| DFFITS | `t·√(h/(1 − h))` |

On the refit they read its design, so they take unpenalized OLS only (`PROCESSING_CONFIG` otherwise). A frequency weight reads each record as `w` identical observations and reports one copy's figure (`h/w`, `df = Σw − p′`, the weighted RSS: statsmodels `OLSInfluence` on the `rep()` expansion); a probability weight is `PULSE_WEIGHT_UNSUPPORTED`. A row outside the fit, or a figure the fit leaves undefined (`df ≤ 1`, `h ≥ 1`), is null.

Each attribute carries its own `target` / `predictors` (plus `penalty` / `alpha` / `l1_ratio`) and refits OLS in its prepass. All four take `params.fit` instead: they name a bound fit — a compose `fits` entry or a loaded `models` entry — and score through it, so a `REG_GLM` residual is `y − μ̂` on the response scale and an extension regression's fit scores through its registration's Scorer. The slot is then row-local and its default label is `<TYPE>_<fit>`. See `skills/op-attr-reg-fitted.md`, `skills/op-attr-reg-residual.md`, `skills/op-attr-reg-leverage.md` and `skills/op-attr-reg-influence.md` for the parameter tables.

### Influence over a bound fit

`ATTR_REG_LEVERAGE`, `ATTR_REG_INFLUENCE` and `ATTR_REG_RESIDUAL` kind `standardized` / `studentized` with `params.fit` read the bound `REG_OLS` / `REG_GLM` fit's design — its `vcov`, its `dispersion` and its `n_obs` — exactly as R 4.6.1's `hatvalues` / `rstandard` / `rstudent` / `cooks.distance` / `dffits` read an `lm` or `glm`. With `x̃ = [1, x…]`, `μ` the fitted mean, `w = (dμ/dη)²/V(μ)` the working weight (1 under OLS), `rP = (y − μ)/√V(μ)` the Pearson residual, `φ` the dispersion (1 for binomial / poisson / negative binomial, whose V(μ) reads the fit's `theta`; `RegressionResult.dispersion` — σ̂² on OLS, the Pearson χ²/df on gamma — where it is estimated), `p′` the coefficient count and `df = n_obs − p′`:

| Figure | Formula |
|---|---|
| leverage `h` | `w · x̃ᵀ(vcov/φ)x̃` |
| standardized | `rP / (√φ·√(1 − h))`; with `params.residual_type` `deviance`, `rD / (√φ·√(1 − h))`, `rD = sign(y − μ)·√dᵢ` the deviance residual (R `rstandard(type = "deviance")`; equal to the Pearson form on a Gaussian fit) |
| σ₍ᵢ₎ (φ estimated) | `√((φ·df − rP²/(1 − h))/(df − 1))` |
| studentized | `rP / (σ₍ᵢ₎·√(1 − h))`; the standardized under binomial / poisson / negative binomial |
| Cook's distance | `(rP/(1 − h))²·h/(φ·p′)` |
| DFFITS | `sign(g′(μ))·rP·√h/(σ·(1 − h))`, σ = σ₍ᵢ₎ or √φ |

`residual_type` is kind `standardized`'s alone (`pearson` default, `deviance`): the studentized residual, Cook's distance and DFFITS stay Pearson-based, as in R, and any other kind refuses it (`PULSE_MODEL_SCORE_INVALID` `params_invalid`). DFFITS is the change on the linear-predictor scale, so under a decreasing link — gamma's inverse — it is negative where the residual is positive. Over an OLS fit every formula reduces to the refit's above. The fit must set `vcov: true` and be unweighted, unpenalized, without `selection`, `resample`, `bounds` or a robust `covariance` (`PULSE_MODEL_SCORE_INVALID` reason `params_invalid`, `details.requires` naming the missing piece); a `REG_BAYES_LINEAR` or extension fit is `type_unsupported`. Weighted bound fits are not supported yet: refit with `weight: null`. A model file keeps the dispersion (`model_format_version` 4) or a negative binomial's θ (5); a file an earlier build saved carries none and is refused (`requires: dispersion`) until re-saved.

## Error codes

Look up full prose via `pulse_errors_lookup` or `pulse errors lookup CODE`.

A fit that fails on its data — `RANK_DEFICIENT`, `SEPARATION`,
`NO_CONVERGE`, `SINGULAR_GRAM`, `INSUFFICIENT_DATA` — fails its slot
alone: `regressions[i]` keeps `name` / `type` and carries `error`
`{code, message, details}`, the other slots of the response survive,
and a `PULSE_SLOT_FAILED` warning names it. Under `Options.Strict` the
run fails with the fit's own code instead. The configuration codes
(`INVALID_FAMILY`, `INVALID_LINK`, `PROCESSING_CONFIG`) stay fatal.
See [Payload JSON Schema](contract/payload-schema.md) (Failed slots).

| Code                                          | Meaning (one-liner)                                                           |
|-----------------------------------------------|-------------------------------------------------------------------------------|
| `PROCESSING_REGRESSION_NOT_IMPLEMENTED`       | Reserved as of Phase 8; no engine returns this today.                         |
| `PROCESSING_REGRESSION_RANK_DEFICIENT`        | XᵀX is singular; add regularization or drop a predictor.                       |
| `PROCESSING_REGRESSION_SEPARATION`            | Binomial `REG_GLM` is separated (a predictor splits the outcome); `details.predictors`. |
| `PROCESSING_REGRESSION_NO_CONVERGE`           | IRLS or coordinate descent failed within `MaxIters`.                          |
| `PROCESSING_REGRESSION_SINGULAR_GRAM`         | XᵀX non-invertible even after regularization; increase `alpha`.               |
| `PROCESSING_REGRESSION_INVALID_FAMILY`        | `REG_GLM` Family outside `{binomial, poisson, gamma, negative_binomial}`.     |
| `PROCESSING_REGRESSION_INVALID_LINK`          | Link not one of the chosen Family's (`details.allowed`).                      |
| `PROCESSING_REGRESSION_INSUFFICIENT_DATA`     | Filtered set has fewer rows than predictors + 1, or below resample minimum.   |
| `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` | Advisory: l1 / elasticnet SEs, p-values and intervals are `null` (no valid analytical form); set `resample` for replicate ones. |
| `PULSE_ADVISORY_CONSTRAINT_ACTIVE` | Advisory: a `REG_OLS` `bounds` constraint binds, so SEs, p-values, intervals and `vcov` are `null`; set `resample: bootstrap` for replicate ones. |
| `PROCESSING_REGRESSION_REGULARIZED_SELECTION` | Warning: `penalty != ""` plus `selection != ""` is unusual.                   |
| `PROCESSING_CONFIG`                           | Invalid spec combination (e.g. Bayes + Resample, gamma GLM + Penalty, `theta` off negative_binomial). |

## Worked examples

Every Indeed name has a runnable JSON file under `internal/examples/regression/`. Fetch via `pulse_examples_get` or read directly:

- [01_ecological_fallacy.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/01_ecological_fallacy.json) — per-region aggregation + ecological caveat (#12).
- [02_simple_linear.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/02_simple_linear.json) — univariate OLS (#1, #3).
- [03_multiple_linear.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/03_multiple_linear.json) — multivariate OLS (#2, #4).
- [04_logistic.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/04_logistic.json) — binary classification (#5).
- [05_ridge.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/05_ridge.json) — l2 penalty (#6).
- [06_lasso.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/06_lasso.json) — l1 penalty (#7).
- [07_polynomial.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/07_polynomial.json) — `FEAT_POLY` + OLS (#8).
- [08_bayesian_linear.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/08_bayesian_linear.json) — conjugate NIG (#9).
- [09_jackknife.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/09_jackknife.json) — leave-one-out resampling (#10).
- [10_elasticnet.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/10_elasticnet.json) — combined l1 / l2 penalty (#11).
- [11_stepwise.json](https://github.com/frankbardon/pulse/blob/main/internal/examples/regression/11_stepwise.json) — BIC-driven stepwise selection (#13).
