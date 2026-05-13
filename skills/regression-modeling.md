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

Phase 3 has landed: `REG_GLM` ships with binomial+logit (logistic) and poisson+log via iteratively-reweighted least squares (IRLS). Gamma+inverse is wired but not numerically validated this phase. `REG_BAYES_LINEAR` still returns `PROCESSING_REGRESSION_NOT_IMPLEMENTED` until Phase 4.

| Phase | Engine                                          | Status   |
|-------|-------------------------------------------------|----------|
| 1     | OLS streaming (no penalty)                      | shipped  |
| 2     | OLS regularization (ridge / lasso / elastic-net)| shipped  |
| 3     | GLM (logistic, poisson)                         | shipped  |
| 4     | Bayesian linear (conjugate NIG)                 | pending  |
| 5     | Modifiers (`Resample`, `Selection`)             | pending  |
| 6     | Compositional coverage (FEAT_POLY, ecological)  | pending  |
| 7     | Per-row regression attributes (ATTR_REG_*)      | pending  |
| 8     | Docs, examples, manifest polish                 | pending  |

Until the engine for a given operator lands, every request slot of that type returns `PROCESSING_REGRESSION_NOT_IMPLEMENTED`.

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

## Modifiers

Two orthogonal modifiers apply to any of the three operators. Both downgrade streaming when set.

### `Resample`

| Value          | Behavior                                        |
|----------------|-------------------------------------------------|
| `""`           | No resampling. Closed-form / asymptotic std errors. |
| `"jackknife"`  | Leave-one-out resampling.                       |
| `"bootstrap"`  | Non-parametric bootstrap (`bootstrap_iters`, `rng_seed`). |

### `Selection`

| Value         | Behavior                                            |
|---------------|-----------------------------------------------------|
| `""`          | No subset selection. Fit on all predictors.         |
| `"forward"`   | Forward selection driven by `criterion ∈ {aic, bic}`. |
| `"backward"`  | Backward elimination driven by `criterion`.         |
| `"stepwise"`  | Bidirectional stepwise; combines forward + backward steps. |

`Selection` requires `Criterion` to be set; predict flags the missing pair with `SERVICE_VALIDATION`.

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

Phases 1–2 light up streaming for both unpenalized and regularized REG_OLS. Bayes (Phase 4) and the modifier wrappers (Phase 5) remain buffered until their phases ship.

## Compositional patterns

### Polynomial regression (`FEAT_POLY` upstream)

Polynomial regression is linear in the coefficients; the non-linearity lives in the feature space. Ship the polynomial expansion as a `FEAT_POLY` operator (Phase 6) running before `REG_OLS`:

```json
{
  "features": [
    {"type": "FEAT_POLY", "field": "x", "params": {"degree": 3}}
  ],
  "regressions": [
    {"type": "REG_OLS", "target": "y", "predictors": ["x", "x^2", "x^3"]}
  ]
}
```

`FEAT_POLY` is reserved here; it lands in Phase 6 with the rest of the engineering wiring.

### Ecological regression (group → regress)

"Ecological regression" is a methodological caveat, not an engine variant. Run a normal request that aggregates per group, then a second request that regresses on the per-group means. Use `pulse_compose` to chain them:

```json
{
  "requests": [
    {
      "cohort": {"filename": "voters.pulse"},
      "groups": [{"type": "GROUP_CATEGORY", "field": "county"}],
      "aggregations": [
        {"type": "AGG_AVERAGE", "field": "income", "label": "mean_income"},
        {"type": "AGG_AVERAGE", "field": "turnout", "label": "mean_turnout"}
      ]
    },
    {
      "cohort": {"filename": "voters.pulse"},
      "regressions": [
        {"type": "REG_OLS", "target": "mean_turnout", "predictors": ["mean_income"]}
      ]
    }
  ]
}
```

The ecological fallacy applies: relationships at the group level need not hold at the individual level. Document this caveat in any consumer-facing prose; the engine cannot enforce it.

## Error codes

`pulse_errors_lookup` returns prose for each:

| Code                                       | When it fires                                                       |
|--------------------------------------------|---------------------------------------------------------------------|
| `PROCESSING_REGRESSION_NOT_IMPLEMENTED`    | Engine not yet shipped (today: `REG_BAYES_LINEAR`, modifier-wrapped specs). |
| `PROCESSING_REGRESSION_RANK_DEFICIENT`     | XᵀX is singular; add regularization or drop a predictor.            |
| `PROCESSING_REGRESSION_NO_CONVERGE`        | IRLS or coordinate descent failed within `MaxIters`. Raise `MaxIters` or `Tol`, or reduce `Alpha`. |
| `PROCESSING_REGRESSION_SINGULAR_GRAM`      | XᵀX non-invertible even after regularization; increase Alpha.       |
| `PROCESSING_REGRESSION_INVALID_FAMILY`     | `REG_GLM` Family outside `{binomial, poisson, gamma}`.              |
| `PROCESSING_REGRESSION_INVALID_LINK`       | `Link` incompatible with the chosen Family.                         |
| `PROCESSING_REGRESSION_INSUFFICIENT_DATA`  | Filtered set has fewer rows than predictors + 1.                    |
| `PROCESSING_CONFIG`                        | Invalid spec combination, e.g. `Penalty="elasticnet"` with `L1Ratio=0` (use `Penalty="l2"` instead). |

Additionally, `PROCESSING_REGRESSION_APPROXIMATE_SE` is emitted as an envelope **warning** (not an error) for `Penalty="l1"` and `Penalty="elasticnet"` fits to flag that the reported SE / p-value entries are plug-in approximations over the active set.

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
