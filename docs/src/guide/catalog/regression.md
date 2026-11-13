# Regressions

The regressions this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`REG_BAYES_LINEAR`](#op-reg_bayes_linear) | Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval. | Given the data and our prior belief, what range of values is plausible for the price coefficient? | advanced | [`REG_OLS`](#op-reg_ols) when you want p-values, resampled standard errors or stepwise predictor selection (none run on this type).<br>[`REG_GLM`](#op-reg_glm) when the outcome is yes/no (0/1) or a count.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you only want how closely two numeric fields follow a straight line together. |
| [`REG_GLM`](#op-reg_glm) | Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts. | Which customer traits are associated with the chance of churning, holding the others fixed? | advanced | [`REG_OLS`](#op-reg_ols) when the outcome is a numeric measure with roughly symmetric, constant-spread residuals.<br>[`TEST_PROP_Z`](test.md#op-test_prop_z) when you only want to know whether a yes/no rate differs between two groups.<br>[`TEST_CHISQ`](test.md#op-test_chisq) when the outcome and the single predictor are both categories. |
| [`REG_MMM_BAYES`](#op-reg_mmm_bayes) | Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls. | What return does each marketing channel earn, and how sure are we? | advanced | [`REG_MMM_HILL`](#op-reg_mmm_hill) when a fast point estimate over already-adstocked channels is enough, or the fit must be saved, scored or optimised. |
| [`REG_MMM_HILL`](#op-reg_mmm_hill) | Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls. | What does each marketing channel contribute to sales, holding price and the other channels fixed? | advanced | [`REG_OLS`](#op-reg_ols) when the outcome moves in a straight line with spend, with no saturation. |
| [`REG_MULTINOMIAL`](#op-reg_multinomial) | Fits a model of an outcome with three or more categories (which plan, brand or channel), comparing each with a reference category. | Which customer traits are associated with choosing the premium or the basic plan over the standard one, holding the others fixed? | advanced | [`REG_GLM`](#op-reg_glm) when the outcome has two categories (yes/no).<br>[`REG_OLS`](#op-reg_ols) when the outcome is numeric.<br>[`TEST_CHISQ`](test.md#op-test_chisq) when you only want to know whether the categories are spread differently across a grouping category. |
| [`REG_OLS`](#op-reg_ols) | Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares. | Which of price, discount and season are associated with weekly sales, holding the others fixed? | intermediate | [`REG_GLM`](#op-reg_glm) when the outcome is yes/no (0/1) or a count.<br>[`REG_BAYES_LINEAR`](#op-reg_bayes_linear) when you want intervals that combine the data with prior knowledge about the coefficients.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you only want how closely two numeric fields follow a straight line together.<br>[`TEST_ANOVA_F`](test.md#op-test_anova_f) when the predictor is a set of groups and you want to know whether the group averages differ.<br>[`ATTR_REG_RESIDUAL`](attribute.md#op-attr_reg_residual) when you want each row's residual, to check the residual assumptions or find rows the model fits badly. |
| [`REG_ORDINAL`](#op-reg_ordinal) | Fits a model that predicts an ordered rating (poor, fair, good) from numeric predictors, each shifting the whole scale up or down. | Which customer traits are associated with a higher satisfaction rating, holding the others fixed? | advanced | [`REG_GLM`](#op-reg_glm) when the outcome has only two values (yes/no).<br>[`REG_OLS`](#op-reg_ols) when the outcome is a numeric measure whose steps are evenly spaced.<br>[`TEST_KRUSKAL_WALLIS`](test.md#op-test_kruskal_wallis) when you only want to know whether the ratings differ between groups.<br>[`TEST_CHISQ`](test.md#op-test_chisq) when the outcome and the single predictor are both categories. |

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
- The link must suit the outcome (each family takes its own: logit, probit or cloglog for binomial; log, identity or sqrt for counts; inverse, log or identity for gamma); the predictors act linearly on that link scale, and margins converts the effects to the outcome's units.
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

**Glossary:** [`deviance`](../glossary.md#term-deviance), [`generalized-linear-model`](../glossary.md#term-generalized-linear-model), [`independence`](../glossary.md#term-independence), [`link-function`](../glossary.md#term-link-function), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`logistic-regression`](../glossary.md#term-logistic-regression), [`marginal-effect`](../glossary.md#term-marginal-effect), [`multicollinearity`](../glossary.md#term-multicollinearity), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`odds-ratio`](../glossary.md#term-odds-ratio), [`overdispersion`](../glossary.md#term-overdispersion), [`p-value`](../glossary.md#term-p-value), [`pseudo-r-squared`](../glossary.md#term-pseudo-r-squared), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`skew`](../glossary.md#term-skew), [`standard-error`](../glossary.md#term-standard-error)

**Validated against:**

- R 4.6.1 `glm` (binomial-logit, poisson-log, gamma-inverse) — tolerance 1e-10 relative; `internal/processing/regression/glm_irls_test.go`, `internal/processing/regression/glm_gamma_oracle_test.go`
- R 4.6.1 `glm` (binomial probit / cloglog, poisson identity / sqrt, gamma log / identity: coefficients, standard errors, vcov, deviance, null deviance, AIC, dispersion, fitted means, working residuals), refitted to its fixed point — tolerance 1e-9 relative; `internal/processing/regression/glm_links_oracle_test.go`
- R 4.6.1 `glm(weights = f)` against its `rep()`-expanded fit (probit, poisson sqrt, gamma log; frequency weights) — tolerance 1e-9 relative; `internal/processing/regression/glm_links_oracle_test.go`
- R 4.6.1 `logLik` / `AIC` of binomial-logit, poisson-log and gamma-inverse `glm` (two_log_lik, aic; gamma's dispersion at deviance / n counted); the non-canonical links' `AIC` against `glm_links.json`, the negative binomial's against `glm_nb.json` — tolerance 1e-10 relative; `internal/processing/regression/loglik_oracle_test.go`, `internal/processing/regression/glm_links_oracle_test.go`
- R 4.6.1 `logLik` / `AIC` of `glm(weights = f)` against its `rep()`-expanded fit (binomial, poisson, gamma; frequency weights) — tolerance 1e-10 relative; `internal/processing/regression/loglik_oracle_test.go`
- R 4.6.1 `glmnet` (penalties) — tolerance 1e-10 relative; `internal/processing/regression/glm_penalty_test.go`
- R 4.6.1 `sandwich` (robust standard errors; vcov) — tolerance 1e-11 relative; `internal/service/regression_covariance_glm_test.go`, `internal/service/regression_vcov_test.go`
- R 4.6.1 `sandwich` 3.1-3 `vcovHC` HC0 / HC1 / HC3 on the non-canonical links — tolerance 1e-9 relative; `internal/processing/regression/glm_links_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 `glm.nb` (negative binomial, θ estimated on log / sqrt / identity, pushed to its fixed point) and `glm(negative.binomial(θ))` (θ fixed): coefficients, standard errors and vcov at dispersion 1, θ, SE(θ), twice the log-likelihood, AIC, deviance, null deviance, fitted means, residuals; `sandwich` 3.1-3 `vcovHC` HC0 / HC1 / HC3 — tolerance 1e-9 relative; `internal/processing/regression/glm_nb_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 `glm.nb(weights = f)` against its `rep()`-expanded fit (negative binomial, log link; frequency weights) — tolerance 1e-9 relative; `internal/processing/regression/glm_nb_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 `summary(glm(negative.binomial(θ)))` (θ fixed, `dispersion: estimated`, log / sqrt link): φ̂ = Pearson χ²/df, vcov and standard errors scaled by it, t p-values on df, Wald-t intervals — tolerance 1e-9 relative; `internal/processing/regression/glm_nb_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 `summary(glm(negative.binomial(θ)))` on the `rep()`-expanded fit (θ fixed, `dispersion: estimated`; frequency weights) — tolerance 1e-9 relative; `internal/processing/regression/glm_nb_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 + `sandwich` 3.1-3: analytic marginal effects (`margins`: ame / mem, response / link scale) of logit, probit, cloglog, poisson, gamma inverse / log and negative binomial `glm` fits — the mean of β·μ′(η), the delta-method Jacobian on `vcov`, `vcovHC` HC3 and `vcovCL` — with statistics, p-values and intervals; cross-checked against `marginaleffects` 1.0.0 `avg_slopes` / `slopes(newdata = "mean")` at 1e-7 — tolerance 1e-9 relative; `internal/processing/regression/margins_oracle_test.go`
- R 4.6.1 analytic marginal effects of `glm(weights = f)` (logit ame / mem) against its `rep()`-expanded fit, and `marginaleffects` `avg_slopes(wts = "f")` — tolerance 1e-9 relative; `internal/processing/regression/margins_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 + `car` 3.1-5 `vif` (`collinearity`: VIF, tolerance, generalized VIF of a factor's dummies, df, GVIF^(1/(2 df))) on binomial-logit, poisson-log, fixed-θ negative binomial and gamma-log `glm` fits refitted to their fixed point (basis model: the IRLS working weights), and on `lm` over the same predictors and prior weights (basis predictors); end to end through `FEAT_ONE_HOT` — tolerance 1e-10 relative; `internal/processing/regression/vif_oracle_test.go`, `collinearity_test.go`
- R 4.6.1 + `car` 3.1-5 `vif(glm(weights = w))`: frequency weights against the `rep()`-expanded fit (logit, poisson basis predictors), probability weights (gamma log) scale-free — tolerance 1e-10 relative; `internal/processing/regression/vif_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 + `sandwich` 3.1-3 + `lmtest` 0.9-40: the Compose nested-model comparison (`compare`) of two `glm` fits refitted to their fixed point — `anova(m0, m1, test = "LRT" | "F")` on logit, probit, poisson, gamma inverse / log and fixed-θ negative binomial fits (the full fit's Pearson dispersion), MASS `anova.negbin` (θ estimated, 2 loglik change; cross-checked against `lrtest`), `waldtest(m0, m1, vcov = vcov | vcovHC(type = "HC3"), test = "Chisq" | "F")` — tolerance 1e-10 relative; `internal/service/compose_compare_oracle_test.go`, `compare_test.go`
- R 4.6.1 `anova` of the `rep()`-expanded `glm` fits (poisson LRT, gamma log F; frequency weights) — tolerance 1e-10 relative; `internal/service/compose_compare_oracle_test.go`
- R 4.6.1 on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights) — tolerance 1e-12 relative; `internal/service/weight_reference_values_test.go`, `internal/service/weight_reference_tests_test.go`

**Validation gap:** Penalized fits on a non-canonical link are checked by their KKT conditions, not against glmnet. Off the canonical link IRLS converges linearly, so at the default tol (1e-8) the coefficients sit within about 1e-7 of the MLE; the oracle fits at tol 1e-13. The negative binomial refuses penalty, selection and resample (no oracle); its probability-weighted fit is checked for scale invariance only. Marginal effects are slopes only (no discrete factor contrasts: regressions take no factor predictors; a 0/1 dummy gets its slope, where marginaleffects reports a 1 − 0 contrast, so that cross-check skips it) and are refused over a predictor derived from another predictor, a penalty, selection, resample or bounds. The collinearity block's penalized basis-predictors figure is checked equal to the unpenalized one (a design figure), not against an external penalized VIF. A nested-model comparison under a probability weight runs the Wald test only (F / LRT refused), checked against its own covariance rather than an external survey test.

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
- Each channel is raw spend, carried over inside the model over max_lag periods by one adstock kernel (geometric by default; delayed or Weibull when the effect peaks later), so its shape is estimated with everything else.
- Each channel's effect follows one Hill curve, rising from 0 and flattening toward its coefficient; the controls enter in a straight line.
- The priors shape the answer where the data are thin: an experiment's ROI is the honest prior to set.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_MMM_HILL`](#op-reg_mmm_hill) when a fast point estimate over already-adstocked channels is enough, or the fit must be saved, scored or optimised.

**Glossary:** [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`regression-coefficient`](../glossary.md#term-regression-coefficient)

**Validated against:**

- `REG_BAYES_LINEAR`'s closed-form conjugate posterior (R 4.6.1 matrix algebra), in the conjugate limit: adstock and curve pinned, flat priors (posterior means, SDs and σ), under each adstock kernel (geometric, delayed, weibull_cdf, weibull_pdf) with the Hill curve and under the logistic and Michaelis–Menten curves — tolerance within 4 Monte Carlo standard errors, seeded; `internal/regmmm/bayes_conjugate_heavy_test.go`
- simulation-based calibration (Talts et al. 2018) on the default priors of every adstock kernel × saturation curve cell (4 × 3), and with a simulated lift test in the likelihood on two cells (geometric × hill, delayed × logistic): 200 seeded replicates per run, rank of the truth among 99 thinned draws — tolerance uniform rank histogram per parameter (χ² test, p ≥ 0.001 / parameters); `internal/regmmm/bayes_sbc_heavy_test.go`
- `WIN_DISTRIBUTED_LAG`'s kernel weights (R 4.6.1 `decay^((l − peak)^2)`, `pweibull`, `dweibull` via its oracle) for the in-model adstock; each saturation curve's closed form and finite-difference partials; a closed-form log posterior and a finite-difference gradient for each kernel and curve, with and without lift tests (a closed-form window Δ in ROI and β mode) — tolerance bit-equal weights; curves 1e-14 absolute, partials 1e-7 relative; density and lift Δ 1e-9 relative; gradient 1e-6 relative; `internal/regmmm/bayes_adstock_test.go`, `internal/regmmm/bayes_curve_test.go`, `internal/regmmm/bayes_model_test.go`, `internal/regmmm/bayes_lift_test.go`
- Stan (CmdStan 2.40.0 via cmdstanr 0.9.0, R 4.6.1) sampling the same model and default priors, written independently, on stored simulated series: one case per adstock kernel (geometric, delayed, weibull_cdf, weibull_pdf) × saturation curve (hill, logistic, michaelis_menten) cell, twelve in all, plus two with a lift test in the likelihood (a switch-off on geometric × hill, a +50% test on delayed × logistic) (posterior means of every coefficient and each channel's ROI) — tolerance within 4.5 combined Monte Carlo standard errors in every case, seeded; `internal/regmmm/bayes_stan_oracle_heavy_test.go`
- synthetic data with known true parameters (internal/mmmfixture) — tolerance parameter recovery; `internal/regmmm/bayes_test.go`

**Validation gap:** The Stan cross-check covers one channel and one control per kernel × curve cell at the default priors, and one lift test on two cells; custom priors, β mode, several channels, several lift tests and the model-owned holdout are covered by the closed-form and calibration checks only. The calibration runs with the outcome scale fixed, because the default priors are stated on the max-scaled outcome and so move with the data.

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

- `minpack.lm::nlsLM` 1.2-4 on the same objective, start and bounds (coefficients, standard errors, R², residual SE) — tolerance 1e-6 relative (the solver's stop rule); `internal/regmmm/hill_oracle_test.go`
- synthetic data with known true parameters (internal/mmmfixture) — tolerance 0.5% to 15% relative, per parameter; `internal/regmmm/hill_test.go`

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-mmm-hill`](../skills/op-reg-mmm-hill.md)

<a id="op-reg_multinomial"></a>

### `REG_MULTINOMIAL`

Fits a model of an outcome with three or more categories (which plan, brand or channel), comparing each with a reference category.

**Level:** advanced

**Also known as:** `multinomial logistic regression`, `multinomial logit`, `mlogit`, `multinom`

**Questions it answers:**

- Which customer traits are associated with choosing the premium or the basic plan over the standard one, holding the others fixed?
- How does the chance of each support channel being picked change with the customer's age?

**Use cases by domain:**

- *survey:* Relate the brand a respondent prefers to their age and income, against a reference brand.
- *ops:* Model which of several resolution paths a ticket takes from its size and queue.
- *science:* Relate the class an observation falls in to measured covariates.

**Assumptions:**

- Rows are independent of each other: repeated rows for the same customer, or values over time, make the reported uncertainty too small.
- The target is a categorical field: its levels, in dictionary order, are the categories; a level no fitted row carries is dropped.
- Each category other than the reference has its own coefficients: the change in the log odds of that category against the reference per unit of the predictor, holding the others fixed.
- The odds between any two categories do not depend on the other categories offered (independence of irrelevant alternatives).
- Each p-value tests the null hypothesis that its coefficient is 0 given the other predictors, as a Wald z.
- If a predictor separates a category from the reference perfectly, the fit cannot converge and Pulse refuses it.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Strongly related predictors (multicollinearity) make coefficients unstable and their uncertainty large; Pulse refuses an exactly collinear set.
- Predictors must be numeric: encode a category as 0/1 columns first (FEAT_ONE_HOT) and leave one level out, or the columns are collinear with the intercept.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_GLM`](#op-reg_glm) when the outcome has two categories (yes/no).
- [`REG_OLS`](#op-reg_ols) when the outcome is numeric.
- [`TEST_CHISQ`](test.md#op-test_chisq) when you only want to know whether the categories are spread differently across a grouping category.

**Glossary:** [`deviance`](../glossary.md#term-deviance), [`independence`](../glossary.md#term-independence), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`logistic-regression`](../glossary.md#term-logistic-regression), [`multicollinearity`](../glossary.md#term-multicollinearity), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`odds-ratio`](../glossary.md#term-odds-ratio), [`p-value`](../glossary.md#term-p-value), [`pseudo-r-squared`](../glossary.md#term-pseudo-r-squared), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`standard-error`](../glossary.md#term-standard-error)

**Validated against:**

- R 4.6.1 + `nnet` 7.3-21 `multinom`, Newton-polished to the MLE: coefficients, standard errors (`solve(Hessian)`), Wald p-values and intervals, vcov, deviance, null deviance, two_log_lik, aic and every row's fitted probabilities; a named reference, a dictionary level no row carries (dropped) — tolerance 1e-10 relative; `internal/processing/regression/multinomial_oracle_test.go`
- R 4.6.1 + `nnet` 7.3-21 `multinom(weights = f)` against its `rep()`-expanded fit (frequency weights) and `multinom(weights = w*)`, w* = w·n_eff/Σw (probability weights, no likelihood) — tolerance 1e-10 relative; `internal/processing/regression/multinomial_oracle_test.go`
- R 4.6.1 on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights) — tolerance 1e-12 relative; `internal/service/weight_reference_values_test.go`, `internal/service/weight_reference_tests_test.go`

**Validation gap:** No robust (sandwich) covariance, penalty, selection or resample is offered: each is refused, not validated. The probability-weighted fit is multinom on the rescaled weights, not a design-based survey estimator.

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-multinomial`](../skills/op-reg-multinomial.md)

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

- NIST StRD certified values (linear regression: Norris, Pontius, NoInt1–2 (intercept: false), Filip, Longley, Wampler1–5) — tolerance digits achieved per dataset (see the NIST table); `strd_reference_test.go`
- R 4.6.1 `lm` — tolerance 1e-10 relative; `internal/processing/regression/ols_lm_reference_test.go`
- R 4.6.1 `glmnet` (penalties), `nnls` (non-negative) — tolerance 1e-10 relative; `internal/processing/regression/ols_penalty_reference_test.go`, `internal/processing/regression/ols_nnls_reference_test.go`
- R 4.6.1 `sandwich` / `lmtest` (HC0–HC3, cluster, HAC standard errors; vcov) — tolerance 1e-10 relative; `internal/service/regression_covariance_test.go`, `internal/service/regression_vcov_test.go`
- R 4.6.1 `logLik` / `AIC` of `lm` with and without an intercept (two_log_lik, aic; σ counted) — tolerance 1e-10 relative; `internal/processing/regression/loglik_oracle_test.go`
- R 4.6.1 `logLik` / `AIC` of the `rep()`-expanded `lm` (frequency weights; `lm(weights = f)` reads them as precision weights) — tolerance 1e-10 relative; `internal/processing/regression/loglik_oracle_test.go`
- R 4.6.1 + MASS 7.3-66 + `sandwich` 3.1-3: analytic marginal effects (`margins`, ame / mem) of `lm` with their delta-method standard errors, t statistics, p-values and intervals; cross-checked against `marginaleffects` 1.0.0 `avg_slopes` / `slopes(newdata = "mean")` at 1e-7 — tolerance 1e-9 relative; `internal/processing/regression/margins_oracle_test.go`
- R 4.6.1 + `car` 3.1-5 `vif` on `lm` (`collinearity`: VIF, tolerance and, over a factor's dummies grouped as one term, Fox & Monette's generalized VIF, df and GVIF^(1/(2 df)); the closed form re-derived from the weighted design) — tolerance 1e-10 relative; `internal/processing/regression/vif_oracle_test.go`, `collinearity_test.go`
- R 4.6.1 + `car` 3.1-5 `vif(lm(weights = w))`: frequency weights against the `rep()`-expanded fit, probability weights scale-free — tolerance 1e-10 relative; `internal/processing/regression/vif_oracle_test.go`
- R 4.6.1 + `sandwich` 3.1-3 + `lmtest` 0.9-40: the Compose nested-model comparison (`compare`) of two `lm` fits — `anova(m0, m1)` F and `test = "Chisq"`, `waldtest(m0, m1, vcov = vcov | vcovHC(type = "HC3"))`, a restricted fit through the origin; statistic, df, p-value, each fit's residual df and RSS, σ̂² — tolerance 1e-10 relative; `internal/service/compose_compare_oracle_test.go`, `compare_test.go`
- R 4.6.1 `anova` of the `rep()`-expanded `lm` fits (frequency weights; df = Σw − rank) — tolerance 1e-10 relative; `internal/service/compose_compare_oracle_test.go`
- R 4.6.1 on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights) — tolerance 1e-12 relative; `internal/service/weight_reference_values_test.go`, `internal/service/weight_reference_tests_test.go`

**Validation gap:** On NIST Wampler5 the coefficients reach 5.7 digits, level with LAPACK's QR (5.6): the double-precision limit for its large residuals.

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-ols`](../skills/op-reg-ols.md)

<a id="op-reg_ordinal"></a>

### `REG_ORDINAL`

Fits a model that predicts an ordered rating (poor, fair, good) from numeric predictors, each shifting the whole scale up or down.

**Level:** advanced

**Also known as:** `ordinal logistic regression`, `ordered logit`, `proportional odds model`, `polr`

**Questions it answers:**

- Which customer traits are associated with a higher satisfaction rating, holding the others fixed?
- How much more likely is a top rating per extra year of tenure?
- Does each predictor shift every step of the rating scale by the same amount?

**Use cases by domain:**

- *survey:* Relate a 1-5 agreement or satisfaction rating to respondent attributes without treating the scale as evenly spaced numbers.
- *ops:* Model a ticket's severity grade (low, medium, high, critical) from its queue and size.
- *science:* Relate a graded response (none, mild, moderate, severe) to dose and age.

**Assumptions:**

- Rows are independent of each other: repeated rows for the same customer, or values over time, make the reported uncertainty too small.
- The levels are ordered: params.levels gives the order, else a categorical's dictionary order or a number's ascending values; an unordered outcome needs another model.
- Proportional odds: each predictor moves every threshold of the scale by the same amount (one coefficient per predictor); params.brant tests it, and a small Brant p-value says some effect differs across the scale.
- The link sets the shape of the scale's steps: logit (default) reads coefficients as log odds ratios, probit as shifts of a normal latent score, cloglog as log hazard ratios.
- Each p-value tests the null hypothesis that its coefficient is 0 given the other predictors, a Wald z on the fitted information.
- A predictor that separates the levels perfectly makes the fit unable to converge, and Pulse refuses it.
- A coefficient describes association with the outcome, holding the other predictors fixed; it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, and a predictor left out of the model can drive both.
- Strongly related predictors (multicollinearity) make coefficients unstable and their uncertainty large; Pulse refuses an exactly collinear set.
- Predictors must be numeric: encode a category as 0/1 columns first (FEAT_ONE_HOT) and leave one level out, or the columns are collinear with the intercept.
- Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used.

**Use something else:**

- [`REG_GLM`](#op-reg_glm) when the outcome has only two values (yes/no).
- [`REG_OLS`](#op-reg_ols) when the outcome is a numeric measure whose steps are evenly spaced.
- [`TEST_KRUSKAL_WALLIS`](test.md#op-test_kruskal_wallis) when you only want to know whether the ratings differ between groups.
- [`TEST_CHISQ`](test.md#op-test_chisq) when the outcome and the single predictor are both categories.

**Glossary:** [`deviance`](../glossary.md#term-deviance), [`independence`](../glossary.md#term-independence), [`link-function`](../glossary.md#term-link-function), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`multicollinearity`](../glossary.md#term-multicollinearity), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`odds-ratio`](../glossary.md#term-odds-ratio), [`p-value`](../glossary.md#term-p-value), [`pseudo-r-squared`](../glossary.md#term-pseudo-r-squared), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`standard-error`](../glossary.md#term-standard-error)

**Validated against:**

- R 4.6.1 + MASS 7.3-66 `polr` (logit / probit / cloglog), polished by Newton to its MLE: coefficients, thresholds, standard errors and vcov from the analytic observed information (polr's numerical Hessian agrees to 1e-4), deviance, null deviance, two_log_lik, AIC, the mean fitted probabilities (`colMeans(fitted())`); `sandwich` 3.1-3 `sandwich` / `vcovCL` (HC0, HC1, CR1); `brant` 0.3.0 (omnibus and per-predictor χ², the binary logits at their fixed point); `params.levels` dropping an unlisted response as `factor(y, levels =)` does — tolerance 1e-10 relative; `internal/regordinal/oracle_test.go`, `internal/regordinal/ordinal_e2e_test.go`
- R 4.6.1 + MASS 7.3-66 `polr(weights = f)` against its `rep()`-expanded fit, and the Brant test on the expansion (frequency weights) — tolerance 1e-10 relative; `internal/regordinal/oracle_test.go`, `internal/regordinal/ordinal_e2e_test.go`
- R 4.6.1 + MASS 7.3-66 `polr(weights = w*)`, w* = w·n_eff/Σw (probability weights: the frequency formulas on w*, no likelihood figures) — tolerance 1e-10 relative; `internal/regordinal/oracle_test.go`, `internal/regordinal/ordinal_e2e_test.go`

**Validation gap:** The Brant test's omnibus statistic reads the symmetric covariance of the binary logits' slopes (Brant 1990); brant 0.3.0 stores the (m, l) cross block untransposed at (l, m), so its omnibus figure differs (per-predictor statistics agree) — the oracle script reproduces the package's figure exactly and pins the symmetric one. Resample, selection, penalty, margins and collinearity are refused (no oracle), and covariance hc2 / hc3 / hac (no row-wise hat matrix, no series).

See [How Pulse is validated](../validation.md).

**Skill:** [`op-reg-ordinal`](../skills/op-reg-ordinal.md)
