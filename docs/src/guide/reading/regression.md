# Reading regression results

How to read each output field of the regressions this instance offers. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

## Regressions

<a id="op-reg_bayes_linear"></a>

### `REG_BAYES_LINEAR`

Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval. See its [catalog entry](../catalog/regression.md#op-reg_bayes_linear).

#### `coefficients.*`

Posterior mean of the coefficient: the expected difference in the outcome between rows one unit apart on this predictor, holding the other predictors fixed, after combining the data with the prior.

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- With the default weak prior and enough rows it is close to the REG_OLS coefficient; a stronger prior pulls it toward prior_mu (0 by default).
- Its size depends on the predictor's units, so do not rank predictors by raw coefficients unless they share units; standardize the predictors first to compare them.
- (intercept) is the prediction when every predictor is 0, which may lie outside the data and mean nothing.

#### `std_errors.*`

Scale of the coefficient's Student-t posterior: how uncertain the coefficient is given the data and the prior. With many rows it is close to the posterior standard deviation.

**Caveats:**

- It is a posterior spread, not a frequentist standard error, and no p-value is built from it.

#### `credible_intervals.*`

[lower, upper] equal-tailed posterior interval at credible_level (95% by default): given the prior and the model, the coefficient lies in this range with that probability.

**Caveats:**

- It is a probability statement about the coefficient conditional on the prior and the model, not a statement about repeated samples; if either is wrong, so is the probability.
- With the default weak prior and many rows per predictor it is close to the OLS confidence interval. With few rows it is narrower, because the default variance prior divides by about n rather than n - p - 1 (about 19% narrower at 20 rows and 5 predictors). With an informative prior it also reflects that prior.
- An interval that excludes 0 is not a significance test; report the interval itself.

#### `r2`

R-squared of the posterior-mean fit: the share of the outcome's variation the posterior-mean coefficients account for in these rows.

**Caveats:**

- It is computed from the posterior-mean coefficients, not averaged over the posterior, so it is not a Bayesian R-squared.
- A strong prior pulls the fit away from least squares, so it can sit below the REG_OLS value and even below 0; there are no published bands for it, so none are attached.

#### `adj_r2`

The posterior-mean fit's R-squared corrected for the number of predictors: 1 - (1 - r2) * (n - 1) / (n - p - 1).

**Caveats:**

- It can fall below 0 when the predictors fit no better than chance would.
- There are no published bands for adjusted R-squared, so none are attached: Cohen's R-squared benchmarks are stated for R-squared itself.

#### `residual_std_err`

Typical size of a residual, in the outcome's units: how far actual values sit from the fitted line on a typical row. Here it is sqrt(b_n / a_n) from the posterior on the variance, combining the data with the prior on it.

**Caveats:**

- Under the default prior that is about sqrt(RSS / n), so it runs below REG_OLS's value (which divides by n - p - 1), most visibly with few rows per predictor; do not read the gap as a better fit.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

<a id="op-reg_glm"></a>

### `REG_GLM`

Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts. See its [catalog entry](../catalog/regression.md#op-reg_glm).

#### `coefficients.*`

The change in the linear predictor, on the link scale, for a one-unit increase in this predictor, holding the other predictors fixed: log-odds for binomial (logit link), the log of the expected count for Poisson (log link), the inverse of the expected value for gamma (inverse link).

**Sign:**

- `+`: higher predictor values are associated with a higher probability (binomial) or expected count (Poisson); under gamma's inverse link, with a LOWER expected value
- `-`: higher predictor values are associated with a lower probability (binomial) or expected count (Poisson); under gamma's inverse link, with a HIGHER expected value

**Caveats:**

- Raised to the power e, a binomial coefficient is an odds ratio and a Poisson one a rate ratio per one-unit increase: multiplicative, not additive. An odds ratio is not a ratio of probabilities.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- Its size depends on the predictor's units, so do not rank predictors by raw coefficients unless they share units; standardize the predictors first to compare them.
- With strongly related predictors (multicollinearity) a coefficient can swing, or even flip sign, between fits on similar data.
- A very large coefficient with a very large standard error in a logistic model is a sign of near-separation; do not read it as a strong association.

#### `std_errors.*`

How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix, with the dispersion fixed at 1.

**Caveats:**

- With overdispersion (counts varying more than their mean) the Poisson standard errors are too small.
- Gamma data rarely have dispersion 1, so gamma standard errors are unreliable.

#### `p_values.*`

**Caveats:**

- Wald z at dispersion fixed to 1: with overdispersed counts the Poisson p-values come out too small, and gamma p-values are unreliable.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `deviance`

How far the fitted predictions sit from the observed outcomes, as twice the log-likelihood gap to a model that fits every row exactly; lower is closer.

**Caveats:**

- It has no fixed scale: compare it only with null_deviance or with another model fitted to the same rows and outcome.
- For a 0/1 outcome the deviance alone does not show whether the model fits.

#### `null_deviance`

The deviance of the intercept-only model, which predicts the overall mean for every row: the baseline the predictors are judged against.

#### `pseudo_r2`

1 - deviance / null_deviance: the share of the intercept-only model's deviance the predictors remove, from 0 to 1. For a 0/1 binomial outcome this equals McFadden's pseudo-R-squared.

**Caveats:**

- It is not a share of variance explained and runs much lower than an OLS R-squared for an equally useful model, so OLS rules of thumb do not apply; there are no published bands for it, so none are attached.
- Compare it only across models of the same outcome and family.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Iteratively reweighted least squares steps taken before the coefficients stopped changing (relative change below tol).

**Caveats:**

- A fit that cannot converge within max_iters is refused rather than reported.

<a id="op-reg_ols"></a>

### `REG_OLS`

Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares. See its [catalog entry](../catalog/regression.md#op-reg_ols).

#### `coefficients.*`

The difference in the outcome's expected value between rows one unit apart on this predictor, holding the other predictors fixed, in outcome units per predictor unit (an association, not the effect of changing it).

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- Its size depends on the predictor's units, so do not rank predictors by raw coefficients unless they share units; standardize the predictors first to compare them.
- With strongly related predictors (multicollinearity) a coefficient can swing, or even flip sign, between fits on similar data.
- (intercept) is the prediction when every predictor is 0, which may lie outside the data and mean nothing.
- Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 on purpose; an l1 coefficient shrunk to 0 means the penalty dropped it, not that the predictor is unrelated to the outcome.

#### `std_errors.*`

How much the coefficient would vary from sample to sample; smaller means a more precise estimate. Pulse reports the classic standard error, which assumes residuals of constant spread.

**Caveats:**

- With heteroscedasticity (residual spread that changes) it can be too small or too large; with dependent rows it is usually too small.
- For l1 and elasticnet fits it is a plug-in approximation over the kept predictors (Pulse warns); request resample for an empirical one.

#### `p_values.*`

**Caveats:**

- The request's alpha param is the penalty strength for l1 / l2 / elasticnet fits, not a significance level; Pulse applies no significance threshold to these p-values.
- On a penalized fit (l1, l2, elasticnet) the coefficients are shrunk on purpose, so these p-values do not have the usual reading; l1 and elasticnet ones are approximate.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `r2`

R-squared: the share of the outcome's variation around its mean that the fitted model accounts for in these rows, from 0 to 1.

**Bands** (convention: Cohen (1988) f-squared benchmarks converted via R2 = f2/(1+f2); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.02 | very small |
| from 0.02 to below 0.13 | small |
| from 0.13 to below 0.26 | medium |
| 0.26 and above | large |

**Caveats:**

- It never falls when a predictor is added, even a useless one; compare models of different sizes with adj_r2.
- It is measured on the rows the model was fitted to, so it overstates how well the model will predict new rows.
- The bands are Cohen's benchmarks for a population R-squared, set for behavioural-science research as a last resort; what counts as a good fit depends on the field and the outcome.
- A high value does not mean the model is right or that the predictors cause the outcome.

#### `adj_r2`

R-squared corrected for the number of predictors: 1 - (1 - r2) * (n - 1) / (n - p - 1), for p predictors.

**Caveats:**

- It can fall below 0 when the predictors fit no better than chance would.
- There are no published bands for adjusted R-squared, so none are attached: Cohen's R-squared benchmarks are stated for R-squared itself.

#### `residual_std_err`

Typical size of a residual, in the outcome's units: how far actual values sit from the fitted line on a typical row. It divides the residual sum of squares by n - p - 1.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Coordinate-descent iterations an l1 or elasticnet fit took to converge; absent for fits solved in closed form.

**Caveats:**

- A value at max_iters suggests the fit stopped before settling; raise max_iters or tol.
