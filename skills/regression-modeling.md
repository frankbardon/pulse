---
name: regression-modeling
description: Fit OLS (with optional l1/l2/elasticnet penalty), GLM (logistic, poisson, gamma), and Bayesian linear regression. Use when a request needs coefficient estimates, R²/deviance, std errors, or per-row residuals; covers the 13 textbook regression names (Simple/Multiple/Linear/Logistic/Ridge/Lasso/Polynomial/Bayesian/Jackknife/ElasticNet/Ecological/Stepwise) via 3 operators + 2 modifiers + FEAT_POLY composition.
type: guide
applies_to: ["process", "compose", "predict"]
---

# Regression modeling

Pulse exposes regression as a small, composable surface. Three top-level operators cover every textbook regression variant once you combine them with two spec-level modifiers, one upstream feature operator (`FEAT_POLY`, Phase 6), and one compositional pattern (group-aggregate-then-regress for ecological regression).

The 13 names in the Indeed taxonomy double-count (Simple = Linear univariate, Multiple = Multiple Linear) and treat orthogonal wrappers (Jackknife, Stepwise) as families. Pulse does not. Use the mapping table below to translate a textbook name into the corresponding Pulse spec.

## Phase status

Phase 5 has landed: `Resample` and `Selection` modifiers apply to any regression operator. Jackknife and bootstrap replace analytical SE with resample-based estimates; forward / backward / stepwise selection drives AIC- or BIC-based greedy subset search. Both modifiers force the buffered orchestrator path.

| Phase | Engine                                          | Status   |
|-------|-------------------------------------------------|----------|
| 1     | OLS streaming (no penalty)                      | shipped  |
| 2     | OLS regularization (ridge / lasso / elastic-net)| shipped  |
| 3     | GLM (logistic, poisson)                         | shipped  |
| 4     | Bayesian linear (conjugate NIG)                 | shipped  |
| 5     | Modifiers (`Resample`, `Selection`)             | shipped  |
| 6     | Compositional coverage (FEAT_POLY, ecological)  | shipped  |
| 7     | Per-row regression attributes (ATTR_REG_*)      | pending  |
| 8     | Docs, examples, manifest polish                 | pending  |

Until the engine for a given operator lands, every request slot of that type returns `PROCESSING_REGRESSION_NOT_IMPLEMENTED`. After Phase 4, only modifier-wrapped specs (`Resample`, `Selection`) on any base operator surface that code.

## Operators

### `REG_OLS` — ordinary least squares

Streaming over sufficient statistics. The Welford accumulator collects `(n, μ_x, μ_y, M2_xx, M2_xy, M2_yy)` in a single pass; the finalize-time solver branches on `Penalty`:

- `Penalty == ""` — Cholesky on the centered Gram (Phase 1).
- `Penalty == "l2"` — Cholesky on the augmented Gram `M2_xx + n·λ·I` (Phase 2).
- `Penalty == "l1" | "elasticnet"` — coordinate descent on the standardized Gram with soft-thresholding (Phase 2). All inner products derive from the Gram; coordinate descent never re-reads the data.

```json
{
  "type": "REG_OLS",
  "target": "y",
  "predictors": ["x1", "x2"]
}
```

Optional regularization:

| `Penalty`     | Meaning              | Extra params           | Solver                |
|---------------|----------------------|------------------------|-----------------------|
| `""`          | No regularization    | —                      | closed-form Cholesky  |
| `"l2"`        | Ridge                | `alpha > 0`            | closed-form Cholesky on `M2_xx + n·λ·I` |
| `"l1"`        | Lasso                | `alpha > 0`            | coordinate descent + soft-threshold |
| `"elasticnet"`| Elastic Net          | `alpha > 0`, `0 < l1_ratio < 1` | coordinate descent (combined penalty) |

`alpha` follows the scikit-learn convention: the penalty is added to the unscaled residual sum of squares. `Ridge(alpha=λ)` and `Lasso(alpha=λ)` on the same data produce the same coefficients as Pulse's `REG_OLS{Penalty:"l2", Alpha:λ}` / `REG_OLS{Penalty:"l1", Alpha:λ}` (to ≥ 1e-3 once predictors are standardized internally).

Convergence knobs for the coordinate-descent solvers:

| Field       | Default | Meaning                                              |
|-------------|---------|------------------------------------------------------|
| `max_iters` | 1000    | Cycle cap. Non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE`. |
| `tol`       | 1e-6    | Stop when `max\|Δβ\|` per cycle falls below this.    |

**Standard error caveat (L1-penalized fits).** The analytical SE / p-value entries for `l1` and `elasticnet` reflect a coarse plug-in over the data-dependent active set; coefficients shrunk to zero have no SE entry. Predict attaches a `PROCESSING_REGRESSION_APPROXIMATE_SE` warning to the envelope for these specs. For rigorous uncertainty quantification, use `Resample: "bootstrap"` or `Resample: "jackknife"` (Phase 5). Ridge and unpenalized OLS retain the standard asymptotic SE formula.

**Recipes.**

```json
// Ridge regression (Indeed #6)
{"type": "REG_OLS", "target": "y", "predictors": ["x1", "x2"], "penalty": "l2", "alpha": 0.1}

// Lasso regression (Indeed #7)
{"type": "REG_OLS", "target": "y", "predictors": ["x1", "x2", "x3"], "penalty": "l1", "alpha": 0.05}

// Elastic Net (Indeed #11)
{"type": "REG_OLS", "target": "y", "predictors": ["x1", "x2", "x3"],
 "penalty": "elasticnet", "alpha": 0.05, "l1_ratio": 0.5}
```

### `REG_GLM` — generalized linear model

Always buffered: iteratively-reweighted least squares (IRLS) reweights each row per iteration, so streaming sufficient stats don't work. The engine buffers the design matrix `X` (n × (p+1) with intercept column) and target `y` once at finalize, then iterates in memory.

```json
{
  "type": "REG_GLM",
  "target": "converted",
  "predictors": ["age", "spend"],
  "family": "binomial",
  "link": "logit"
}
```

#### IRLS algorithm

For a GLM with link `g` and variance function `V`, the engine starts from an intercept-only seed `η₀ = g(ȳ_safe)`, then on each iteration computes the working response and diagonal weights row-by-row:

```
μ_i  = g⁻¹(η_i)
dμdη = ∂μ/∂η at η_i
W_i  = dμdη² / V(μ_i)
z_i  = η_i + (y_i − μ_i) / dμdη
β_new = (XᵀWX)⁻¹ XᵀWz       # weighted normal equations via Cholesky
```

The loop stops when `||Δβ|| / (||β|| + ε) < Tol`. Defaults: `MaxIters = 50`, `Tol = 1e-8`; both honor `RegressionSpec.MaxIters` / `RegressionSpec.Tol` overrides. Non-convergence (separable logistic data, ill-conditioned poisson rates, etc.) surfaces `PROCESSING_REGRESSION_NO_CONVERGE`.

#### Family / link compatibility

| `Family`    | Default `Link` | Implemented (Phase 3) | Reserved enum values                |
|-------------|----------------|------------------------|--------------------------------------|
| `binomial`  | `logit`        | `logit`                | `probit`, `cloglog` (deferred)       |
| `poisson`   | `log`          | `log`                  | `identity`, `sqrt` (deferred)        |
| `gamma`     | `inverse`      | `inverse` (wired, not numerically validated) | `log`, `identity` (deferred) |

| Family    | g(μ)         | g⁻¹(η)        | dμ/dη       | V(μ)        |
|-----------|--------------|---------------|-------------|-------------|
| binomial  | log(μ/(1−μ)) | 1/(1+e⁻ⁿ)     | μ(1−μ)      | μ(1−μ)      |
| poisson   | log(μ)       | eⁿ            | eⁿ = μ      | μ           |
| gamma     | 1/μ          | 1/η           | −μ²         | μ²          |

Misuse surfaces `PROCESSING_REGRESSION_INVALID_FAMILY` (unknown / empty family) or `PROCESSING_REGRESSION_INVALID_LINK` (requested link incompatible with the family, or a reserved-but-not-implemented combination such as `binomial+probit`).

#### Standard errors and inference

`Cov(β) = (XᵀWX)⁻¹` at the converged weights. For `binomial` and `poisson` the dispersion parameter is fixed at 1 by the family assumption, so the engine emits a **Wald-z** p-value: `p = erfc(|β/SE| / √2)` — the two-sided tail of the standard normal, not a Student's t. Gamma's dispersion is not estimated this phase; its Wald-z values are emitted on the same dispersion=1 assumption and should be treated skeptically until the gamma family ships with its own validation.

`Deviance` and `NullDeviance` are populated; `PseudoR2` is the McFadden-style `1 − Deviance / NullDeviance`. `ResidualStdErr` is not meaningful for GLM (no residual variance estimate); leave it at zero in consumer prose.

#### Recipes

Logistic regression (Indeed #5) — binary classification with covariates:

```json
{"type": "REG_GLM", "target": "converted", "predictors": ["age", "spend"],
 "family": "binomial", "link": "logit"}
```

Poisson regression — event count modeling:

```json
{"type": "REG_GLM", "target": "click_count", "predictors": ["impressions", "ctr_lag"],
 "family": "poisson", "link": "log"}
```

#### Deferred this phase

- **GLM + penalty** (regularized logistic, lasso-penalized Poisson, etc.) — setting `Penalty`, `Alpha`, or `L1Ratio` on a `REG_GLM` spec is rejected with `PROCESSING_CONFIG` rather than silently ignored. Regularized GLM lands in a later phase.
- **Gamma numerical fixtures** — the link function is wired correctly but its coefficient recovery is not exercised against an external oracle. Use it experimentally; report regressions.
- **Modifier composition** (`Resample`, `Selection`) — same deferred status as REG_OLS; setting either falls through to the not-implemented stub until Phase 5.

### `REG_BAYES_LINEAR` — Bayesian linear regression

Streaming over sufficient statistics (the same as `REG_OLS`) followed by a closed-form conjugate posterior update.

```json
{
  "type": "REG_BAYES_LINEAR",
  "target": "y",
  "predictors": ["x1", "x2"],
  "prior": "nig",
  "credible_level": 0.95
}
```

Only `prior: "nig"` (Normal-Inverse-Gamma) is supported in v1. MCMC / variational alternatives are reviewed in Phase 9 (planning only).

#### Conjugate NIG posterior

Prior on `(β, σ²)`:

```
σ²    ~ InverseGamma(a₀, b₀)
β | σ² ~ Normal(μ₀, σ² · Λ₀⁻¹)
```

After observing `n` rows on intercepted design `X` (size `n × (p+1)`; column 0 is the intercept) and target `y`, the posterior is also NIG:

```
Λ_n = Λ₀ + XᵀX
μ_n = Λ_n⁻¹ · (Λ₀ μ₀ + Xᵀy)
a_n = a₀ + n/2
b_n = b₀ + ½ (yᵀy + μ₀ᵀΛ₀μ₀ − μ_nᵀΛ_nμ_n)
```

`XᵀX`, `Xᵀy`, `yᵀy`, and `n` are sufficient statistics — the engine recovers each from the Welford-centered moments produced by the same accumulator that powers `REG_OLS`, then performs a single finalize-time Cholesky solve on `Λ_n`.

#### Prior parameterization

| Field            | Default | Meaning                                                                                  |
|------------------|---------|------------------------------------------------------------------------------------------|
| `prior`          | `"nig"` | Prior family. Empty defaults to `"nig"`; no other values are accepted in v1.             |
| `prior_mu`       | `0`     | Length `p+1` vector. First entry is the intercept prior; remaining entries map to predictors in `predictors` order. Empty defaults to the zero vector. |
| `prior_precision`| `1e-3`  | Scalar `ε`. Prior precision matrix is `Λ₀ = ε · I`. Must be `> 0` when provided.         |
| `prior_shape`    | `1e-3`  | Inverse-Gamma shape `a₀` on `σ²`. Must be `> 0` when provided.                           |
| `prior_rate`     | `1e-3`  | Inverse-Gamma rate `b₀` on `σ²`. Must be `> 0` when provided.                            |
| `credible_level` | `0.95`  | Mass of the symmetric credible interval reported per coefficient. `0 < level < 1`.       |

**Design choice — scalar prior precision.** `Λ₀` is restricted to a scalar multiple of the identity (`ε · I`). The spec exposes one number (`prior_precision`) rather than a full `(p+1) × (p+1)` matrix. Per-coefficient precisions (different shrinkage strengths per predictor) would require a richer wire shape; if you need it, request the extension via Phase 9. The scalar prior covers every textbook "ridge-as-Bayesian-regression" recipe — set `prior_precision = λ / σ²_prior_mean` to recover a ridge fit with strength `λ`.

The defaults `(μ₀ = 0, ε = 1e-3, a₀ = b₀ = 1e-3)` are weakly informative: the prior contributes roughly `ε` of the per-coefficient precision and `b₀` of the residual variance scale. For any data set with `n ≥ ~50` rows and non-pathological predictor scales, the posterior mean tracks the OLS estimate to within several digits.

#### Output

| Field                | Meaning                                                                                       |
|----------------------|-----------------------------------------------------------------------------------------------|
| `coefficients`       | Posterior means `μ_n`. Keys: `"(intercept)"` plus each predictor name.                        |
| `std_errors`         | Posterior std under the Student-t marginal: `√((b_n/a_n) · (Λ_n⁻¹)[j,j])`.                    |
| `credible_intervals` | `[lower, upper] = μ_n[j] ± t_q · SE[j]` with `t_q = qt(1 − (1−level)/2, df = 2·a_n)`.         |
| `r2` / `adj_r2`      | Computed from the posterior-mean point estimate using the usual RSS / TSS identities.         |
| `residual_std_err`   | Posterior-mean estimate of `σ`: `√(b_n / a_n)`.                                              |
| `n_obs`              | Observations after listwise null deletion.                                                    |
| `prior`              | Echoes the chosen prior family (always `"nig"` in v1).                                        |
| `converged_iters`    | `0` — closed-form, no iteration.                                                              |

`p_values` is intentionally **not emitted**. Credible intervals replace frequentist p-values in the Bayesian setting. Mixing the two would invite hybrid interpretations that the math does not support; treat the `credible_intervals` map as the authoritative uncertainty summary.

#### Reading credible intervals

A 95% credible interval for `β_j` is the interval that contains 95% of the posterior probability mass for that coefficient — in plain English, "given the data and the prior, there is a 95% probability that β_j lies in [lower, upper]". This is a different statement from a 95% confidence interval (which is a statement about the long-run frequency of a procedure, not about the parameter). When the prior is diffuse and the model well-specified the two intervals are numerically close, but the interpretation diverges.

#### Recipes

Diffuse Bayesian fit (matches OLS for any reasonable `n`):

```json
{"type": "REG_BAYES_LINEAR", "target": "y", "predictors": ["x1", "x2"]}
```

Shrinkage prior centered at zero (ridge-equivalent):

```json
{"type": "REG_BAYES_LINEAR", "target": "y", "predictors": ["x1", "x2"],
 "prior_precision": 1.0}
```

Informed prior with nonzero means (treatment-effect carryover, e.g. each coefficient is expected to be ~0.5):

```json
{"type": "REG_BAYES_LINEAR", "target": "y", "predictors": ["x1", "x2"],
 "prior_mu": [0.0, 0.5, 0.5], "prior_precision": 0.1}
```

90% credible intervals instead of the 95% default:

```json
{"type": "REG_BAYES_LINEAR", "target": "y", "predictors": ["x1", "x2"],
 "credible_level": 0.90}
```

#### Deferred this phase

- Non-conjugate priors (`horseshoe-vb`, `spike-slab`, etc.) — would need VB or MCMC; Phase 9 review.
- Full prior precision matrix (`Λ₀` as a `(p+1) × (p+1)` matrix instead of a scalar) — Phase 9 review.
- Hierarchical / multilevel structure — out of scope; tracked in `README.md`'s "Out of scope" section.
- Modifier composition (`Resample`, `Selection`) — same deferred status as REG_OLS / REG_GLM; setting either falls through to the not-implemented stub until Phase 5.

Setting `Penalty`, `Alpha`, `L1Ratio`, `Family`, or `Link` on a `REG_BAYES_LINEAR` spec is rejected with `PROCESSING_CONFIG` rather than silently ignored — those knobs belong to other engines.

## Modifiers

Two orthogonal modifiers apply to `REG_OLS` (penalized or unpenalized) and `REG_GLM`. `REG_BAYES_LINEAR` rejects both at spec validation — the posterior already conveys uncertainty (credible intervals via the Student-t marginal) and stepwise feature selection on a Bayesian model is a posterior-based question that the conjugate-NIG engine doesn't support.

Both modifiers force the buffered orchestrator path: they refit the model many times and the streaming Welford accumulator can't carry that bookkeeping.

### `Resample` — Indeed #10 (Jackknife)

| Value          | Behavior                                        |
|----------------|-------------------------------------------------|
| `""`           | No resampling. Closed-form / asymptotic std errors. |
| `"jackknife"`  | Leave-one-out resampling. SE = sqrt((n−1)/n · Σᵢ (β⁽⁻ⁱ⁾ − β̄)²). |
| `"bootstrap"`  | Non-parametric bootstrap (`bootstrap_iters`, `rng_seed`). SE = sample std of replicate β's; p-values from percentile method. |

`BootstrapIters` defaults to 1000 (the minimum textbook count for percentile CIs); set higher for deeper tail probabilities. `RNGSeed = 0` time-seeds the RNG; non-zero seeds produce reproducible runs (used by tests).

Resample replaces `StdErrors` / `PValues` in the result and echoes `Resample` for provenance. The point estimate (`Coefficients`) stays at the full-data fit.

For L1 / elasticnet OLS, setting `Resample` is the rigorous answer for standard errors: the Phase 2 `PROCESSING_REGRESSION_APPROXIMATE_SE` warning is suppressed when a resample modifier is present (the warning is no longer applicable — the SEs are now resample-based, not plug-in).

**Recipe (Jackknife on logistic regression):**

```json
{
  "regressions": [
    {
      "type": "REG_GLM",
      "target": "purchased",
      "predictors": ["age", "income", "visits"],
      "family": "binomial",
      "link": "logit",
      "resample": "jackknife"
    }
  ]
}
```

### `Selection` — Indeed #13 (Stepwise)

| Value         | Behavior                                            |
|---------------|-----------------------------------------------------|
| `""`          | No subset selection. Fit on all predictors.         |
| `"forward"`   | Start from intercept-only; add the predictor that lowers the criterion most. |
| `"backward"`  | Start from full model; remove the predictor whose absence lowers the criterion most. |
| `"stepwise"`  | Bidirectional sweep; at each cycle try every add and every remove and apply the best move. |

`Selection` requires `Criterion ∈ {aic, bic}`. AIC = -2·logL + 2·k; BIC = -2·logL + log(n)·k. BIC's heavier per-parameter penalty rejects noise predictors more reliably at moderate sample sizes; AIC may retain predictors whose chance correlation with the response dips RSS by enough to pass a 2-point threshold.

For OLS the log-likelihood is the Gaussian MLE; k counts (slopes + intercept + σ² estimate). For GLM the score is deviance + 2·k (AIC) or deviance + log(n)·k (BIC); the saturated log-likelihood constant cancels between candidates.

Selection populates `SelectedFeatures` with the chosen subset and drops non-selected predictors from `Coefficients` entirely (absent ≠ zero — selection's contract is stronger).

**Recipe (Stepwise OLS):**

```json
{
  "regressions": [
    {
      "type": "REG_OLS",
      "target": "y",
      "predictors": ["x1", "x2", "x3", "x4", "x5"],
      "selection": "stepwise",
      "criterion": "bic"
    }
  ]
}
```

**Composing modifiers.** Selection and Resample can be set together: Selection picks the active subset, then Resample replaces the SE / p-values on the selected model. Use this when you want subset selection plus rigorous uncertainty quantification at the final step.

**Warning: regularized + Selection.** `REG_OLS` with `Penalty != ""` plus `Selection != ""` is accepted but emits `PROCESSING_REGRESSION_REGULARIZED_SELECTION` as a warning. Regularization already does feature shrinkage (l2) or selection (l1); layering greedy subset search on top is unusual and may produce a model harder to interpret than either alone. Regularized + Resample is fine — resample is the rigorous answer for L1 SE and pairs well with the penalty.

## The 13 textbook names → Pulse specs

| #  | Indeed name        | Pulse expression                                                       |
|----|--------------------|------------------------------------------------------------------------|
| 1  | Simple             | `REG_OLS` with one predictor                                           |
| 2  | Multiple           | `REG_OLS` with multiple predictors                                     |
| 3  | Linear             | = #1                                                                   |
| 4  | Multiple Linear    | = #2                                                                   |
| 5  | Logistic           | `REG_GLM{Family:"binomial", Link:"logit"}`                             |
| 6  | Ridge              | `REG_OLS{Penalty:"l2", Alpha:λ}`                                       |
| 7  | Lasso              | `REG_OLS{Penalty:"l1", Alpha:λ}`                                       |
| 8  | Polynomial         | `FEAT_POLY{Field:x, Degree:n}` upstream → `REG_OLS` (Phase 6)          |
| 9  | Bayesian Linear    | `REG_BAYES_LINEAR{Prior:"nig"}`                                        |
| 10 | Jackknife          | any regression with `Resample:"jackknife"`                             |
| 11 | Elastic Net        | `REG_OLS{Penalty:"elasticnet", Alpha, L1Ratio}`                        |
| 12 | Ecological         | `GROUP_*` upstream → `REG_OLS` over group means (composed request)     |
| 13 | Stepwise           | any regression with `Selection:"stepwise", Criterion:"aic"\|"bic"`     |

## Streamability matrix

| Spec                                               | Streamable | Memory  | Notes                              |
|----------------------------------------------------|------------|---------|------------------------------------|
| `REG_OLS` no penalty                               | yes        | O(p²)   | sufficient stats: n, Σx, Σy, XᵀX, Xᵀy, Σy² |
| `REG_OLS` + l1 / l2 / elasticnet                   | yes        | O(p²)   | streaming Gram; regularized solve at finalize |
| `REG_BAYES_LINEAR` (conjugate NIG)                 | yes        | O(p²)   | streaming sufficient stats + prior update     |
| `REG_GLM` (logistic / poisson / gamma)             | no         | O(n·p)  | IRLS / Newton needs multiple passes           |
| Any regression with `Resample != ""`               | no         | O(n·p)  | LOO / bootstrap refit                         |
| Any regression with `Selection != ""`              | no         | O(n·p)  | refit per candidate subset                    |

Phases 1–2 light up streaming for both unpenalized and regularized REG_OLS. Phase 4 lights up streaming for `REG_BAYES_LINEAR` via the same Welford sufficient statistics plus a conjugate posterior update at finalize. Phase 5 ships modifier wrappers that always force buffered — both Resample and Selection refit many times, defeating the streaming pattern.

## Compositional patterns

### Polynomial regression (`FEAT_POLY` upstream)

Polynomial regression is linear in the coefficients; the non-linearity lives in the feature space. Ship the polynomial expansion as `FEAT_POLY` (Phase 6) running before `REG_OLS`:

```json
{
  "features": [
    {"type": "FEAT_POLY", "field": "x", "label": "x", "params": {"degree": 3}}
  ],
  "regressions": [
    {"type": "REG_OLS", "name": "polynomial_fit", "target": "y",
     "predictors": ["x", "x_2", "x_3"]}
  ]
}
```

`FEAT_POLY` emits `Degree - 1` derived columns: `<prefix>_2`, `<prefix>_3`, ..., `<prefix>_<Degree>`. The original column (degree 1) is left in place, so the linear term `x` is reachable to OLS unchanged. With `Label: "x"` the predictors become `["x", "x_2", "x_3"]` (clean and symmetric); omit `Label` and the prefix defaults to `<field>_poly` yielding `["x", "x_poly_2", "x_poly_3"]`. Either works — pick whichever reads better in your downstream tooling. Degree is gated at `[2, 10]`; out-of-range values surface `PROCESSING_CONFIG` from both predict and the engine.

Numerical stability is the caller's responsibility: `x^10` overflows `f64` once `|x|` clears a few hundred, and the Gram matrix conditions poorly even before that. Standardize or center predictors before requesting `FEAT_POLY`. Orthogonal polynomial bases (Chebyshev, Legendre) are not synthesized in Phase 6 — they warrant a dedicated operator and are reserved for a later phase.

See `skills/feature-engineering.md` for the full `FEAT_POLY` parameter table and column-naming rules.

### Ecological regression (group → regress)

"Ecological regression" is a regression fit on **aggregated** group-level statistics — per-precinct means, per-county sums, per-region rates — rather than individual-level rows. The classical setup is: aggregate by some grouping key, then regress one aggregate against another.

#### Pulse expression

Use `pulse_compose` with two slots over the same cohort: slot 1 produces per-group means via `GROUP_*` + `AGG_AVERAGE`, slot 2 fits `REG_OLS` over the aggregate output (or, more commonly in practice, over a pre-aggregated cohort file).

```json
{
  "requests": [
    {
      "cohort": {"filename": "voters.pulse"},
      "groups": [{"type": "GROUP_CATEGORY", "field": "county"}],
      "aggregations": [
        {"type": "AGG_AVERAGE", "field": "income",  "label": "mean_income"},
        {"type": "AGG_AVERAGE", "field": "turnout", "label": "mean_turnout"}
      ]
    },
    {
      "cohort": {"filename": "county_means.pulse"},
      "regressions": [
        {"type": "REG_OLS", "name": "county_ols",
         "target": "mean_turnout", "predictors": ["mean_income"]}
      ]
    }
  ]
}
```

The two slots are intentionally independent: Pulse does not pipe slot-1 result rows into slot-2 as cohort input. You either (a) materialize slot 1's aggregate as its own `.pulse` cohort upstream and pass that filename to slot 2, or (b) accept that slot 1 is the "audit trail" (per-group means visible in the composed response) and run slot 2 over a pre-aggregated fixture. Both are common.

#### Caution — the ecological fallacy

**A significant group-level slope does not imply an individual-level association.** Robinson (1950) showed that ecological correlations and individual correlations can take opposite signs in the same data: a per-state regression of literacy on race might suggest a strong relationship that vanishes (or reverses) at the per-person level. The reason is that aggregation collapses within-group variation, leaving only between-group variation — and the between-group structure can encode confounders that wouldn't survive an individual-level fit.

When ecological regression is the right tool:

- **Aggregate-only data.** The individual-level records are not available (census output, public-health summary tables, election precincts).
- **Group-level question.** The research question is genuinely about groups — "do counties with higher median income have higher turnout?" — not individuals.

When it is the wrong tool:

- **Individual-level claim.** "Higher-income voters turn out more" cannot be inferred from a per-county fit. Use individual-level data.
- **Causal claim.** Group-level confounding (e.g. urbanization correlating with both predictors) routinely produces spurious aggregate slopes.

Annotate any consumer-facing prose with this caveat; Pulse cannot enforce it.

Reference: Robinson, W.S. (1950). "Ecological Correlations and the Behavior of Individuals." *American Sociological Review* 15(3): 351–357.

## Error codes

`pulse_errors_lookup` returns prose for each:

| Code                                       | When it fires                                                       |
|--------------------------------------------|---------------------------------------------------------------------|
| `PROCESSING_REGRESSION_NOT_IMPLEMENTED`    | Reserved; no engine returns this today (every operator + modifier combo ships through Phase 5). |
| `PROCESSING_REGRESSION_RANK_DEFICIENT`     | XᵀX is singular; add regularization or drop a predictor.            |
| `PROCESSING_REGRESSION_NO_CONVERGE`        | IRLS or coordinate descent failed within `MaxIters`. Raise `MaxIters` or `Tol`, or reduce `Alpha`. |
| `PROCESSING_REGRESSION_SINGULAR_GRAM`      | XᵀX non-invertible even after regularization; increase Alpha.       |
| `PROCESSING_REGRESSION_INVALID_FAMILY`     | `REG_GLM` Family outside `{binomial, poisson, gamma}`.              |
| `PROCESSING_REGRESSION_INVALID_LINK`       | `Link` incompatible with the chosen Family.                         |
| `PROCESSING_REGRESSION_INSUFFICIENT_DATA`  | Filtered set has fewer rows than predictors + 1; or jackknife / bootstrap fixture has fewer than 3 rows. |
| `PROCESSING_CONFIG`                        | Invalid spec combination, e.g. `Penalty="elasticnet"` with `L1Ratio=0` (use `Penalty="l2"` instead), or `REG_BAYES_LINEAR` with any `Resample` / `Selection` modifier. |

Additionally:

- `PROCESSING_REGRESSION_APPROXIMATE_SE` is emitted as an envelope **warning** for `Penalty="l1"` and `Penalty="elasticnet"` fits *when no Resample is set*, to flag that the reported SE / p-value entries are plug-in approximations over the active set. Suppressed when `Resample` is set (bootstrap / jackknife is the rigorous answer and replaces the analytical SE entirely).
- `PROCESSING_REGRESSION_REGULARIZED_SELECTION` is emitted as a warning when `REG_OLS` combines a non-empty `Penalty` with a non-empty `Selection`. Regularization already does feature shrinkage / selection; layering greedy subset search on top is unusual.

## Response shape

`Response.Regressions []RegressionResult` carries one entry per `Request.Regressions` slot in matching order. Every field of the struct is declared up front so engines never need to migrate the shape:

```jsonc
{
  "name": "ols_y_on_x1_x2",
  "type": "REG_OLS",
  "coefficients": {"(intercept)": 0.42, "x1": 1.08, "x2": -0.31},
  "std_errors":   {"(intercept)": 0.11, "x1": 0.06, "x2": 0.05},
  "p_values":     {"(intercept)": 0.001, "x1": 1.4e-12, "x2": 8.3e-6},
  "r2": 0.87, "adj_r2": 0.86,
  "n_obs": 1024,
  "residual_std_err": 1.34
  // GLM-only fields: family, link, deviance, null_deviance, pseudo_r2
  // Bayes-only fields: prior, credible_intervals
  // Modifier fields: resample, selection, criterion, selected_features
}
```

Engines populate only the fields relevant to their operator; unused branches carry zero values. Phase 0 stubs never reach this shape — every fit errors out before populating the slot.
