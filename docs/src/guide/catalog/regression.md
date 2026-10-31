# Regressions

The regressions this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`REG_BAYES_LINEAR`](#op-reg_bayes_linear) | Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval. | Given the data and our prior belief, what range of values is plausible for the price coefficient? | advanced | [`REG_OLS`](#op-reg_ols) when you want p-values, resampled standard errors or stepwise predictor selection (none run on this type).<br>[`REG_GLM`](#op-reg_glm) when the outcome is yes/no (0/1) or a count.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you only want how closely two numeric fields follow a straight line together. |
| [`REG_GLM`](#op-reg_glm) | Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts. | Which customer traits are associated with the chance of churning, holding the others fixed? | advanced | [`REG_OLS`](#op-reg_ols) when the outcome is a numeric measure with roughly symmetric, constant-spread residuals.<br>[`TEST_PROP_Z`](test.md#op-test_prop_z) when you only want to know whether a yes/no rate differs between two groups.<br>[`TEST_CHISQ`](test.md#op-test_chisq) when the outcome and the single predictor are both categories. |
| [`REG_MMM_BAYES`](#op-reg_mmm_bayes) | Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls. | What return does each marketing channel earn, and how sure are we? | advanced | [`REG_MMM_HILL`](#op-reg_mmm_hill) when a fast point estimate over already-adstocked channels is enough, or the fit must be saved, scored or optimised. |
| [`REG_MMM_HILL`](#op-reg_mmm_hill) | Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls. | What does each marketing channel contribute to sales, holding price and the other channels fixed? | advanced | [`REG_OLS`](#op-reg_ols) when the outcome moves in a straight line with spend, with no saturation. |
| [`REG_OLS`](#op-reg_ols) | Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares. | Which of price, discount and season are associated with weekly sales, holding the others fixed? | intermediate | [`REG_GLM`](#op-reg_glm) when the outcome is yes/no (0/1) or a count.<br>[`REG_BAYES_LINEAR`](#op-reg_bayes_linear) when you want intervals that combine the data with prior knowledge about the coefficients.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you only want how closely two numeric fields follow a straight line together.<br>[`TEST_ANOVA_F`](test.md#op-test_anova_f) when the predictor is a set of groups and you want to know whether the group averages differ.<br>[`ATTR_REG_RESIDUAL`](attribute.md#op-attr_reg_residual) when you want each row's residual, to check the residual assumptions or find rows the model fits badly. |

## Operators

<a id="op-reg_bayes_linear"></a>

### `REG_BAYES_LINEAR`

Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval.

**Level:** advanced

**Also known as:** `bayesian linear regression`

**Questions it answers:**

- Given the data and our prior belief, what range of values is plausible for the price coefficient?
- How do the coefficients shift when last year's estimates are used as the prior?

**Use cases by domain:**

- *survey:* Fit a key-driver model on a small wave, using the previous wave's coefficients as the prior mean.
- *ops:* Relate cost per order to volume when only a few weeks of data exist.
- *science:* Combine a small new study with earlier estimates of a dose-response slope.

**Assumptions:**

- Rows are independent of each other: repeated rows for the same customer, or values over time, make the reported uncertainty too small.
- The outcome changes linearly with each predictor, with normal residuals of constant spread (the model's likelihood).
- The prior is conjugate Normal-Inverse-Gamma: each coefficient is normal around prior_mu (zeros by default, intercept first) with one shared precision for every coefficient, intercept included, measured relative to the residual variance: each coefficient's prior standard deviation is σ / sqrt(prior_precision), so the prior's strength changes with the noise level. The residual variance is inverse-gamma.
- The default prior is weak (precision 0.001), so with enough rows the coefficients sit close to REG_OLS. Its pull depends on each predictor's units: a coefficient that is large because its predictor's units are small is pulled harder toward prior_mu.
- Credible intervals are probability statements about each coefficient given the prior and the model; a wrong prior or a wrong model makes them wrong too.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Strongly related predictors (multicollinearity) make coefficients unstable; the prior keeps even an exactly collinear set solvable, but then the prior, not the data, decides how the shared association is split between them.
- Predictors must be numeric: encode a category as 0/1 columns first (FEAT_ONE_HOT) and leave one level out, or the columns are collinear with the intercept.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_OLS`](#op-reg_ols) when you want p-values, resampled standard errors or stepwise predictor selection (none run on this type).
- [`REG_GLM`](#op-reg_glm) when the outcome is yes/no (0/1) or a count.
- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you only want how closely two numeric fields follow a straight line together.

**Glossary:** [`credible-interval`](../glossary.md#term-credible-interval), [`heteroscedasticity`](../glossary.md#term-heteroscedasticity), [`independence`](../glossary.md#term-independence), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`multicollinearity`](../glossary.md#term-multicollinearity), [`normal-distribution`](../glossary.md#term-normal-distribution), [`posterior`](../glossary.md#term-posterior), [`prior`](../glossary.md#term-prior), [`r-squared`](../glossary.md#term-r-squared), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`residual`](../glossary.md#term-residual)

**Validated against:**

- R 4.6.1 matrix algebra for the normal-inverse-gamma posterior — tolerance 1e-10 relative; `internal/processing/regression/bayes_reference_test.go`, `internal/processing/regression/bayes_precision_test.go`
- R 4.6.1 on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights) — tolerance 1e-12 relative; `internal/service/weight_reference_values_test.go`, `internal/service/weight_reference_tests_test.go`

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-bayes-linear`](../skills/op-reg-bayes-linear.md)

<a id="op-reg_glm"></a>

### `REG_GLM`

Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts.

**Level:** advanced

**Also known as:** `generalized linear model`, `logistic regression`, `poisson regression`, `glm`

**Questions it answers:**

- Which customer traits are associated with the chance of churning, holding the others fixed?
- How does the expected number of support tickets change with account size?

**Use cases by domain:**

- *survey:* Relate a yes/no answer (would recommend) to respondent attributes.
- *ops:* Model the count of incidents per site from traffic and staffing.
- *science:* Relate the probability of a response to dose and age.

**Assumptions:**

- Rows are independent of each other: repeated rows for the same customer, or values over time, make the reported uncertainty too small.
- The family must match the outcome: binomial needs a 0/1 (or packed_bool) target, Poisson non-negative counts, gamma positive values.
- Only the canonical link runs for each family (logit for binomial, log for Poisson, inverse for gamma); the predictors act linearly on that link scale.
- Each p-value tests the null hypothesis that its coefficient is 0 given the other predictors: a Wald z with the dispersion fixed at 1 for binomial and Poisson, where overdispersed counts (variance above the mean) make Poisson p-values too small; a Student t on the residual df for gamma, whose dispersion is estimated from the data as R's glm does.
- If a predictor separates the 0s from the 1s perfectly in a logistic model, the fit cannot converge and Pulse refuses it.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Strongly related predictors (multicollinearity) make coefficients unstable and their uncertainty large; Pulse refuses an exactly collinear set.
- Predictors must be numeric: encode a category as 0/1 columns first (FEAT_ONE_HOT) and leave one level out, or the columns are collinear with the intercept.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_OLS`](#op-reg_ols) when the outcome is a numeric measure with roughly symmetric, constant-spread residuals.
- [`TEST_PROP_Z`](test.md#op-test_prop_z) when you only want to know whether a yes/no rate differs between two groups.
- [`TEST_CHISQ`](test.md#op-test_chisq) when the outcome and the single predictor are both categories.

**Glossary:** [`deviance`](../glossary.md#term-deviance), [`generalized-linear-model`](../glossary.md#term-generalized-linear-model), [`independence`](../glossary.md#term-independence), [`link-function`](../glossary.md#term-link-function), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`logistic-regression`](../glossary.md#term-logistic-regression), [`multicollinearity`](../glossary.md#term-multicollinearity), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`odds-ratio`](../glossary.md#term-odds-ratio), [`overdispersion`](../glossary.md#term-overdispersion), [`p-value`](../glossary.md#term-p-value), [`pseudo-r-squared`](../glossary.md#term-pseudo-r-squared), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`skew`](../glossary.md#term-skew), [`standard-error`](../glossary.md#term-standard-error)

**Validated against:**

- R 4.6.1 `glm` (binomial-logit, poisson-log, gamma-inverse) — tolerance 1e-10 relative; `internal/processing/regression/glm_irls_test.go`, `internal/processing/regression/glm_gamma_oracle_test.go`
- R 4.6.1 `glmnet` (penalties) — tolerance 1e-10 relative; `internal/processing/regression/glm_penalty_test.go`
- R 4.6.1 `sandwich` (robust standard errors; vcov) — tolerance 1e-11 relative; `internal/service/regression_covariance_glm_test.go`, `internal/service/regression_vcov_test.go`
- R 4.6.1 on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights) — tolerance 1e-12 relative; `internal/service/weight_reference_values_test.go`, `internal/service/weight_reference_tests_test.go`

**Validation gap:** Only the canonical link of each family is implemented, so only those are validated.

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-glm`](../skills/op-reg-glm.md)

<a id="op-reg_mmm_bayes"></a>

### `REG_MMM_BAYES`

Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls.

**Level:** advanced

**Also known as:** `bayesian media mix model`, `bayesian marketing mix model`, `bayesian mmm`

**Questions it answers:**

- What return does each marketing channel earn, and how sure are we?
- How long does each channel's effect carry over?

**Use cases by domain:**

- *ops:* Which channels earn their spend, with intervals, before moving budget.

**Assumptions:**

- The rows are one market's series, one row per period with contiguous integer times: a gap or a repeated period is refused. Each period's noise is independent of the others once the carry-over is modelled: leftover dependence over time makes the intervals too narrow.
- Each channel is raw spend, carried over geometrically inside the model over max_lag periods, so the decay is estimated with everything else.
- Each channel's effect follows one Hill curve, rising from 0 and flattening toward its coefficient; the controls enter in a straight line.
- The priors shape the answer where the data are thin: an experiment's ROI is the honest prior to set.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_MMM_HILL`](#op-reg_mmm_hill) when a fast point estimate over already-adstocked channels is enough, or the fit must be saved, scored or optimised.

**Glossary:** [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`regression-coefficient`](../glossary.md#term-regression-coefficient)

**Validated against:**

- synthetic data with known true parameters (internal/mmmfixture) — tolerance parameter recovery; `internal/regmmm/bayes_test.go`

**Validation gap:** No external reference implementation; the fit is checked by recovering known parameters only.

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-mmm-bayes`](../skills/op-reg-mmm-bayes.md)

<a id="op-reg_mmm_hill"></a>

### `REG_MMM_HILL`

Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls.

**Level:** advanced

**Also known as:** `media mix model`, `marketing mix model`, `mmm`

**Questions it answers:**

- What does each marketing channel contribute to sales, holding price and the other channels fixed?
- At what weekly spend does each channel saturate?

**Use cases by domain:**

- *ops:* Estimate which channels earn their spend, as the curves a budget split is then optimised over.

**Assumptions:**

- Rows are independent of each other: repeated rows for the same customer, or values over time, make the reported uncertainty too small.
- Each channel is spend already carried over from earlier periods (adstocked upstream, for example by WIN_DISTRIBUTED_LAG): the model does not carry spend over itself, so the carry-over decay is searched outside it.
- Each channel's effect follows one Hill curve, rising from 0 and flattening toward its coefficient; the controls enter in a straight line.
- A channel's coefficient is held at 0 or above by default, so a channel the data do not support comes back exactly 0.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- A channel whose spend never reaches its half-saturation leaves its curve weakly pinned down even when its total contribution is right.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_OLS`](#op-reg_ols) when the outcome moves in a straight line with spend, with no saturation.

**Glossary:** [`independence`](../glossary.md#term-independence), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`standard-error`](../glossary.md#term-standard-error)

**Validated against:**

- synthetic data with known true parameters (internal/mmmfixture) — tolerance 0.5% to 15% relative, per parameter; `internal/regmmm/hill_test.go`

**Validation gap:** No external reference implementation; the fit is checked by recovering known parameters only.

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-mmm-hill`](../skills/op-reg-mmm-hill.md)

<a id="op-reg_ols"></a>

### `REG_OLS`

Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares.

**Level:** intermediate

**Also known as:** `linear regression`, `ordinary least squares`, `lm`

**Questions it answers:**

- Which of price, discount and season are associated with weekly sales, holding the others fixed?
- How much does predicted order value change per extra visit, with the other predictors held fixed?
- How much of the variation in satisfaction do these attribute ratings account for together?

**Use cases by domain:**

- *survey:* Relate overall satisfaction to several attribute ratings at once (often called key-driver analysis, though it shows which ratings go with satisfaction, not which ones drive it).
- *ops:* Model ticket handling time from queue length and agent tenure.
- *science:* Estimate how a measured response changes with dose while adjusting for body weight.

**Assumptions:**

- Rows are independent of each other: repeated rows for the same customer, or values over time, make the reported uncertainty too small.
- The outcome changes linearly with each predictor; model a curve by adding transformed predictors (FEAT_POLY, FEAT_LOG).
- Residuals have roughly constant spread: Pulse reports classic standard errors, not heteroscedasticity-robust ones. With few rows the residuals should also be roughly normal for the p-values to hold.
- Each p-value tests the null hypothesis that its coefficient is 0 given the other predictors, on a t distribution with n - p - 1 degrees of freedom (p predictors).
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Strongly related predictors (multicollinearity) make coefficients unstable and their uncertainty large; Pulse refuses an exactly collinear set.
- Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 by design, so their p-values lose the usual reading; l1 and elasticnet fits report no standard errors or p-values (null) because the data pick the predictors.
- Predictors must be numeric: encode a category as 0/1 columns first (FEAT_ONE_HOT) and leave one level out, or the columns are collinear with the intercept.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_GLM`](#op-reg_glm) when the outcome is yes/no (0/1) or a count.
- [`REG_BAYES_LINEAR`](#op-reg_bayes_linear) when you want intervals that combine the data with prior knowledge about the coefficients.
- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you only want how closely two numeric fields follow a straight line together.
- [`TEST_ANOVA_F`](test.md#op-test_anova_f) when the predictor is a set of groups and you want to know whether the group averages differ.
- [`ATTR_REG_RESIDUAL`](attribute.md#op-attr_reg_residual) when you want each row's residual, to check the residual assumptions or find rows the model fits badly.

**Glossary:** [`adjusted-r-squared`](../glossary.md#term-adjusted-r-squared), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`heteroscedasticity`](../glossary.md#term-heteroscedasticity), [`independence`](../glossary.md#term-independence), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`multicollinearity`](../glossary.md#term-multicollinearity), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`outlier`](../glossary.md#term-outlier), [`overfitting`](../glossary.md#term-overfitting), [`p-value`](../glossary.md#term-p-value), [`r-squared`](../glossary.md#term-r-squared), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`residual`](../glossary.md#term-residual), [`standard-error`](../glossary.md#term-standard-error)

**Validated against:**

- NIST StRD certified values (linear regression: Norris, Pontius, Longley, Wampler1–5) — tolerance digits achieved per dataset (see the NIST table); `strd_reference_test.go`
- R 4.6.1 `lm` — tolerance 1e-10 relative; `internal/processing/regression/ols_lm_reference_test.go`
- R 4.6.1 `glmnet` (penalties), `nnls` (non-negative) — tolerance 1e-10 relative; `internal/processing/regression/ols_penalty_reference_test.go`, `internal/processing/regression/ols_nnls_reference_test.go`
- R 4.6.1 `sandwich` / `lmtest` (HC0–HC3, cluster, HAC standard errors; vcov) — tolerance 1e-10 relative; `internal/service/regression_covariance_test.go`, `internal/service/regression_vcov_test.go`
- R 4.6.1 on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights) — tolerance 1e-12 relative; `internal/service/weight_reference_values_test.go`, `internal/service/weight_reference_tests_test.go`

**Validation gap:** NIST Filip (degree-10 polynomial) is refused as rank-deficient, and NoInt1 / NoInt2 cannot run: REG_OLS has no regression through the origin.

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-ols`](../skills/op-reg-ols.md)
