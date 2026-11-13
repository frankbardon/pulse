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
- Under a robust covariance (hc0-hc3, cluster, hac) it is instead the scale of Muller's sandwich posterior, a normal centred on the same posterior mean whose spread is the sandwich, not the model's: the posterior covariance is REPLACED, so the model's own uncertainty no longer enters.

#### `credible_intervals.*`

[lower, upper] equal-tailed posterior interval at credible_level (95% by default): given the prior and the model, the coefficient lies in this range with that probability.

**Caveats:**

- It is a probability statement about the coefficient conditional on the prior and the model, not a statement about repeated samples; if either is wrong, so is the probability.
- With the default weak prior and many rows per predictor it is close to the OLS confidence interval. With few rows it is narrower, because the default variance prior divides by about n rather than n - p - 1 (about 19% narrower at 20 rows and 5 predictors). With an informative prior it also reflects that prior.
- An interval that excludes 0 is not a significance test; report the interval itself.
- Under a robust covariance it is the sandwich posterior's normal interval (no df), a substitute for the posterior: honest about heteroskedastic or dependent rows, but no longer the conjugate model's probability statement.

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the equal-tailed posterior interval on the coefficient's Student-t posterior with df degrees of freedom, as in credible_intervals.

**Caveats:**

- With conf_level unset it is built at credible_level, so it equals credible_intervals; it reads like them, as a probability given the prior and the model.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the equal-tailed posterior interval on the coefficient's Student-t posterior with df degrees of freedom, as in credible_intervals.

**Caveats:**

- With conf_level unset it is built at credible_level, so it equals credible_intervals; it reads like them, as a probability given the prior and the model.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `df`

Degrees of freedom of the coefficients' Student-t posterior, 2 * a_n, which the interval's critical value reads.

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

The change in the linear predictor, on the link scale, for a one-unit increase in this predictor, holding the other predictors fixed: log-odds for binomial (logit link), the log of the expected count for Poisson (log link), the inverse of the expected value for gamma (inverse link), the log of the expected count for negative_binomial (log link).

**Sign:**

- `+`: higher predictor values are associated with a higher probability (binomial) or expected count (Poisson); under gamma's inverse link, with a LOWER expected value
- `-`: higher predictor values are associated with a lower probability (binomial) or expected count (Poisson); under gamma's inverse link, with a HIGHER expected value

**Caveats:**

- Raised to the power e, a binomial coefficient is an odds ratio and a Poisson one a rate ratio per one-unit increase: multiplicative, not additive. An odds ratio is not a ratio of probabilities.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- Its size depends on the predictor's units, so do not rank predictors by raw coefficients unless they share units; standardize the predictors first to compare them.
- With strongly related predictors (multicollinearity) a coefficient can swing, or even flip sign, between fits on similar data.
- A very large coefficient with a very large standard error in a logistic model is a sign of near-separation; do not read it as a strong association.
- Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 on purpose, on the predictors' own units; an l1 coefficient shrunk to 0 means the penalty dropped it, not that the predictor is unrelated to the outcome.

#### `std_errors.*`

How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix: with the dispersion fixed at 1 for binomial, Poisson and negative_binomial (at the fitted theta), and scaled by the estimated dispersion for gamma (and for a fixed-theta negative_binomial under dispersion estimated).

**Caveats:**

- With overdispersion (counts varying more than their mean) the Poisson standard errors are too small; negative_binomial models it through theta.
- negative_binomial treats the coefficients and theta as independent (MASS summary.negbin), so its standard errors ignore theta's uncertainty.
- The gamma dispersion is estimated as the Pearson chi-square over the residual df, as R's summary.glm does, so gamma standard errors need no dispersion assumption; with no residual df (as many rows as coefficients) they are null.
- Unless the request's covariance names a robust one: hc0-hc3 (sandwich over the fit's scores, which also absorbs overdispersion), cluster (rows grouped by a field) or hac (a series in time); p_values, the intervals and vcov follow it, still Wald z.
- On an l2 fit it is the penalized sandwich, which treats the shrinkage as fixed; on an l1 or elasticnet fit it is null on every coefficient (the data pick the predictors, so no analytical standard error is valid); request resample for replicate ones.

#### `p_values.*`

**Caveats:**

- Binomial and Poisson: Wald z at dispersion fixed to 1, so with overdispersed counts the Poisson p-values come out too small. Gamma (and a fixed-theta negative_binomial under dispersion estimated): a Student t on df, because its dispersion is estimated (R's summary.glm); a robust covariance makes it Wald z again.
- The request's alpha param is the penalty strength, not a significance level; on an l2 fit the coefficients are shrunk on purpose, so these p-values do not have the usual reading, and on an l1 or elasticnet fit they are null.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) a critical value times its standard error, on the link scale: standard normal (a Wald interval) for binomial and Poisson, Student t on df for gamma and a negative_binomial under dispersion estimated.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Binomial and Poisson read dispersion fixed to 1: too narrow with overdispersed counts, and poor near separation in a logistic model.
- Raised to the power e, the bounds of a binomial or Poisson coefficient bound its odds ratio or rate ratio.
- On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) a critical value times its standard error, on the link scale: standard normal (a Wald interval) for binomial and Poisson, Student t on df for gamma and a negative_binomial under dispersion estimated.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Binomial and Poisson read dispersion fixed to 1: too narrow with overdispersed counts, and poor near separation in a logistic model.
- Raised to the power e, the bounds of a binomial or Poisson coefficient bound its odds ratio or rate ratio.
- On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `df`

Gamma (and a fixed-theta negative_binomial under dispersion estimated) only: the residual degrees of freedom n - p - 1 (sum_weights or n_eff in place of n on a weighted fit, so it can be fractional) that the estimated dispersion divides by and the p-values' and intervals' Student t reads.

**Caveats:**

- Absent on binomial and Poisson fits, whose dispersion is fixed at 1, and on any fit under a robust covariance or resample, which use the normal.
- Null when it is 0 or below: no residual df to estimate the dispersion from, so the standard errors, p-values and bounds are null too.

#### `deviance`

How far the fitted predictions sit from the observed outcomes, as twice the log-likelihood gap to a model that fits every row exactly; lower is closer.

**Caveats:**

- On a penalized fit it is the unpenalized deviance at the shrunken coefficients, so it sits above the unpenalized fit's.
- It has no fixed scale: compare it only with null_deviance or with another model fitted to the same rows and outcome.
- For a 0/1 outcome the deviance alone does not show whether the model fits.
- Under selection, binomial and Poisson candidates are scored by deviance plus the parameter penalty; a gamma candidate by R's AIC / BIC (its likelihood at dispersion deviance / n, the dispersion counted as a parameter), so a gamma deviance gap weighs 1 / dispersion times more.

#### `null_deviance`

The deviance of the intercept-only model, which predicts the overall mean for every row: the baseline the predictors are judged against.

#### `pseudo_r2`

1 - deviance / null_deviance: the share of the intercept-only model's deviance the predictors remove, from 0 to 1. For a 0/1 binomial outcome this equals McFadden's pseudo-R-squared.

**Caveats:**

- It is not a share of variance explained and runs much lower than an OLS R-squared for an equally useful model, so OLS rules of thumb do not apply; there are no published bands for it, so none are attached.
- Compare it only across models of the same outcome and family.

#### `theta`

negative_binomial only: the shape theta in Var = mu + mu^2 / theta, the maximum-likelihood estimate (MASS glm.nb) or the fixed theta param. Smaller theta means more overdispersion; a very large theta is the Poisson.

**Caveats:**

- A fit whose estimate runs off to infinity (counts no more variable than Poisson) is refused: fit family poisson instead.

#### `theta_std_err`

negative_binomial with theta estimated: the standard error of theta, from its observed information at the fitted means (MASS's SE.theta).

**Caveats:**

- Theta's sampling distribution is skewed; a Wald interval from this standard error can reach below 0.

#### `two_log_lik`

Twice the maximised log-likelihood of the family at the fitted means (R's logLik on glm; MASS glm.nb's twologlik); higher is a closer fit.

**Caveats:**

- Absent on a probability-weighted fit (a weighted pseudo-likelihood is not a likelihood), a penalized fit, and an OLS fit whose bound binds.
- A Poisson or binomial outcome that is not a whole count has no likelihood: it is null (R's -Inf).

#### `aic`

-two_log_lik + 2 x the parameter count: the coefficients, plus the dispersion for gamma (at deviance / n, as R's Gamma family) and theta for a negative_binomial fit that estimated it (MASS glm.nb's AIC); lower is better.

**Caveats:**

- Compare it only across models of the same outcome fitted to the same rows.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Iteratively reweighted least squares steps taken before the coefficients stopped changing (relative change below tol); a penalized fit counts the same outer steps, a negative_binomial fit with theta estimated the steps of every round.

**Caveats:**

- A fit that cannot converge within max_iters is refused rather than reported.

#### `margins.estimates.*`

The marginal effect of this term (requested by margins): the change in the expected outcome per unit of the predictor on the response scale — probability units for binomial, expected counts for Poisson and negative_binomial — averaged over the fitted rows (ame) or at the predictor means (mem, its point in at). On the link scale, and on an identity link, it is the coefficient itself.

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Unlike a coefficient, a response-scale effect depends on where it is evaluated: ame and mem differ, and the ame describes these rows, not a typical one.
- Standard errors are delta-method (first order); with few rows or fitted values near 0 or 1 they can understate the uncertainty.
- Each effect is a slope: the change for a small change in the predictor, other predictors held fixed. For a 0/1 predictor it is not the discrete difference between its two levels.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- A multiplicity block corrects these p-values as a family of their own, apart from the coefficients'.

#### `margins.std_errors.*`

The effect's delta-method standard error, on the fit's own coefficient covariance (classical, or the request's robust covariance).

#### `margins.p_values.*`

**Caveats:**

- The p-value and interval use the coefficient table's distribution (Student t on df where the coefficients read one, else normal).

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `collinearity.vif.*`

The variance inflation factor of this term (requested by collinearity): how many times larger its coefficient's variance is than it would be if the term were unrelated to the other predictors. 1 means no overlap. For a group of columns (a one-hot block's dummies) it is the generalized VIF, the same idea for the group's coefficients jointly.

**Caveats:**

- No convention bands it. Cut-offs of 5 or 10 are rules of thumb only (O'Brien 2007): a large sample can make a high value harmless, a small one a modest value matter.
- It depends on the predictors (and, on basis model of a GLM, the fitted weights), not on the outcome's values: it says how unstable the coefficients are, not whether a predictor matters.
- A group's generalized VIF grows with its number of columns; compare groups of different size through gvif_scaled, not vif.

#### `collinearity.tolerance.*`

The reciprocal of vif: the share of the term's spread the other predictors do not explain. Near 0 means the term is close to a combination of the others.

#### `collinearity.gvif_scaled.*`

vif^(1/(2 df)): the generalized VIF on the scale of a standard error, so terms of different column counts compare; for a single column it is the square root of vif.

<a id="op-reg_mmm_bayes"></a>

### `REG_MMM_BAYES`

Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls. See its [catalog entry](../catalog/regression.md#op-reg_mmm_bayes).

#### `coefficients.*`

Each coefficient's posterior mean in original units: a channel's coefficient is the most its curve can add to the outcome (the effect at full saturation) and a control's the change in the outcome per unit of the control, each holding the other predictors fixed; the adstock kernel's terms are each channel's carry-over — decay:&lt;channel&gt; (geometric), decay: and peak:&lt;channel&gt; (delayed, peak the lag of greatest effect) or shape: and scale:&lt;channel&gt; (Weibull, scale in periods) — the saturation curve's terms its response shape — k:&lt;channel&gt; and s:&lt;channel&gt; the Hill half-saturation spend and curve shape, lam:&lt;channel&gt; the logistic rate (half-saturated at log 3 / lam), k:&lt;channel&gt; alone the Michaelis–Menten half-saturation — and sigma the noise SD. On a geo panel (params.geo) a pooled term's own name is its population value, &lt;term&gt;[&lt;geo&gt;] the geo's (each geo has its own (intercept)[&lt;geo&gt;]) and tau:&lt;term&gt; how far the geos spread around the population on the parameter's unconstrained scale (log ROI, logit decay).

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- A lift test (params.lift_tests) or an experiment-based roi prior anchors a channel's effect.
- A geo's term is pulled toward the population value, the more so the less its own rows say: a small tau is the data finding the geos alike, not proof they are equal.
- k and s (or lam) trade off against each other on a channel whose spend never reaches its half-saturation: judge it by its ROI and contribution, not by the curve's terms.
- (intercept) is the prediction when every predictor is 0, which may lie outside the data and mean nothing.

#### `std_errors.*`

The posterior standard deviation of each coefficient over every retained draw of every chain: the spread of what the data and the priors allow.

**Caveats:**

- It includes the carry-over decay's uncertainty, which a fit over already-adstocked channels cannot.

#### `p_values.*`

**Caveats:**

- Always null: the model reports no p-value; read the credible intervals in outputs instead.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `statistics.*`

Always null: the model reports no test statistic.

#### `outputs.*`

The posterior's own answers, each summary {mean, median, sd, lower, upper} with [lower, upper] the credible interval at credible_level: roi per channel, the outcome each unit of spend returned over the fitted period against spending nothing on it; mroi per channel, the return on the next unit of spend at the fitted level (below the ROI when the channel saturates); contribution per channel, the outcome it produced over the fitted period; params per coefficient; diagnostics, whether the chains agree and explored the posterior (R-hat at most 1.01, effective sample sizes of at least 400, no divergences or tree-depth hits); holdout, with params.holdout_from, the held-out periods' rmse and the share inside the predictive interval (near credible_level when the intervals are honest); hierarchy, with params.geo, the geos and per pooled term the population value mu and the geos' spread tau, while roi, mroi and contribution add each geo's &lt;channel&gt;[&lt;geo&gt;] beside the all-geo figure (the panel's summed contribution over its summed spend); and seed, the seed the sampler ran with.

**Caveats:**

- A credible interval is conditional on the model and the priors: a wrong adstock window, a missing control or a seasonal confounder moves it without widening it.
- An ROI below 1 means the channel returned less outcome than it cost only when the outcome and the spend are in the same currency.
- mroi is a local slope: it holds the fitted response curve fixed and is not the return of a large budget shift.
- Contributions are attributed by the model; they are not observed.
- A fit that fails a diagnostics check carries a PROCESSING_REGRESSION_DIAGNOSTIC warning naming it; its intervals may be too narrow or off-centre.
- A short holdout gives a noisy coverage: with ten periods each one moves it by a tenth.

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the posterior mean minus (lower) or plus (upper) a normal critical value times the posterior SD, a normal approximation to the posterior.

**Caveats:**

- For a skewed posterior (a decay near 0 or 1, a peak or k on its bound) it can leave the parameter's range: read the credible intervals in outputs.params instead.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the posterior mean minus (lower) or plus (upper) a normal critical value times the posterior SD, a normal approximation to the posterior.

**Caveats:**

- For a skewed posterior (a decay near 0 or 1, a peak or k on its bound) it can leave the parameter's range: read the credible intervals in outputs.params instead.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped; with params.holdout_from, the held-out rows are counted too.

**Caveats:**

- A dropped row leaves a gap in the series, which the model refuses: the series must stay contiguous.

<a id="op-reg_mmm_hill"></a>

### `REG_MMM_HILL`

Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls. See its [catalog entry](../catalog/regression.md#op-reg_mmm_hill).

#### `coefficients.*`

A channel's coefficient is the most its curve can add to the outcome (the effect at full saturation), in outcome units, holding the other predictors fixed; a control's is the change in the outcome per unit of the control, holding the other predictors fixed; k:&lt;channel&gt; is the spend at which the channel delivers half its maximum and s:&lt;channel&gt; how sharply its curve bends there.

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- A channel's coefficient is held at 0 or above by default: exactly 0 means the data do not support an effect, not a proven absence of one.
- A channel whose spend never reaches its k sits on a ridge where only the ratio of its coefficient to k^s is pinned down: judge it by its contribution, not by k or s.
- (intercept) is the prediction when every predictor is 0, which may lie outside the data and mean nothing.

#### `std_errors.*`

How much the parameter would vary from sample to sample, from the curvature of the least-squares fit at its optimum (Gauss-Newton), mapped onto k and s.

**Caveats:**

- Null for a parameter that ends on its bound, and for the k and s of a channel whose coefficient is held at 0: the data cannot inform the shape of a curve that adds nothing.
- It treats the carry-over decay as known; a decay chosen by a search makes the true uncertainty larger.
- With rows ordered in time it is usually too small (see the independence assumption); resample bootstrap gives replicate ones.

#### `p_values.*`

**Caveats:**

- Null on a plain fit: the model reports none itself; under resample each comes from the replicate fits.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `statistics.*`

The Wald statistic: the parameter divided by its standard error, null where the standard error is.

**Caveats:**

- It is not converted to a p-value on a plain fit; a k or s statistic tests a value of 0 the curve never takes, so read it only as precision.

#### `outputs.*`

outputs.k and outputs.s repeat each channel's k:&lt;channel&gt; and s:&lt;channel&gt; coefficients as objects keyed by channel.

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the parameter minus (lower) or plus (upper) a Student-t critical value on df times its standard error.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Null where the standard error is; under resample the critical value is the normal one on the replicate standard error.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the parameter minus (lower) or plus (upper) a Student-t critical value on df times its standard error.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Null where the standard error is; under resample the critical value is the normal one on the replicate standard error.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `df`

Residual degrees of freedom n minus every fitted parameter (intercept, channel coefficients, controls, and each channel's k and s).

#### `r2`

R-squared: the share of the outcome's variation around its mean that the fitted curves and controls account for in these rows.

**Caveats:**

- It is measured on the rows the model was fitted to; a holdout on the last periods shows how well the curves predict.
- There are no published bands for a nonlinear model's R-squared, so none are attached.

#### `residual_std_err`

Typical size of a residual, in the outcome's units: how far actual values sit from the fitted line on a typical row. It divides the residual sum of squares by df.

#### `deviance`

The residual sum of squares at the fit: lower is closer; compare it only across fits of the same rows and outcome.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Least-squares solver iterations taken before the fit settled.

**Caveats:**

- A fit that cannot converge within the iteration cap fails its slot alone rather than being reported.

<a id="op-reg_multinomial"></a>

### `REG_MULTINOMIAL`

Fits a model of an outcome with three or more categories (which plan, brand or channel), comparing each with a reference category. See its [catalog entry](../catalog/regression.md#op-reg_multinomial).

#### `coefficients.*`

Keyed &lt;category&gt;:&lt;term&gt;: the change in the log odds of that category against the reference category for a one-unit increase in the term's predictor, holding the other predictors fixed; &lt;category&gt;:(intercept) is that log odds when every predictor is 0.

**Sign:**

- `+`: higher predictor values are associated with a higher chance of this category relative to the reference, other predictors held fixed
- `-`: higher predictor values are associated with a lower chance of this category relative to the reference, other predictors held fixed

**Caveats:**

- Raised to the power e it is a relative risk ratio (odds of this category against the reference) per one-unit increase: multiplicative, and relative to the reference only. A positive coefficient does not mean the category itself grows more likely: its probability also depends on how the other categories move.
- Every coefficient is against the same reference; comparing two non-reference categories means the difference of their coefficients, or a refit with another reference.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- Its size depends on the predictor's units, so do not rank predictors by raw coefficients unless they share units; standardize the predictors first to compare them.
- With strongly related predictors (multicollinearity) a coefficient can swing, or even flip sign, between fits on similar data.
- A very large coefficient with a very large standard error is a sign of near-separation of that category; do not read it as a strong association.

#### `std_errors.*`

How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix (nnet::multinom's Hessian).

**Caveats:**

- They assume the model is right; no robust (sandwich) form is offered for this model.

#### `p_values.*`

**Caveats:**

- A Wald z for one coefficient; it says nothing about whether the predictor matters for the outcome as a whole, across every category.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) the standard normal critical value times its standard error, on the log-odds scale (a Wald interval).

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Raised to the power e, the bounds bound the relative risk ratio.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) the standard normal critical value times its standard error, on the log-odds scale (a Wald interval).

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Raised to the power e, the bounds bound the relative risk ratio.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `deviance`

-2 times the maximised log-likelihood (nnet::multinom's deviance); lower is closer.

**Caveats:**

- It has no fixed scale: compare it only with null_deviance or with another model fitted to the same rows and outcome.

#### `null_deviance`

The deviance of the intercept-only model, which predicts each category's overall share for every row: the baseline the predictors are judged against.

#### `pseudo_r2`

1 - deviance / null_deviance: McFadden's pseudo-R-squared, the share of the intercept-only model's deviance the predictors remove, from 0 to 1.

**Caveats:**

- It is not a share of variance explained and runs much lower than an OLS R-squared for an equally useful model, so OLS rules of thumb do not apply; there are no published bands for it, so none are attached.

#### `two_log_lik`

Twice the maximised log-likelihood (-deviance); higher is a closer fit.

**Caveats:**

- Absent on a probability-weighted fit (a weighted pseudo-likelihood is not a likelihood).

#### `aic`

-two_log_lik + 2 x the coefficient count (every category's), nnet::multinom's AIC; lower is better.

**Caveats:**

- Compare it only across models of the same outcome fitted to the same rows.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Newton-Raphson steps taken before the coefficients stopped changing (relative change below tol).

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
- Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 on purpose, on the predictors' own units (a predictor on a large scale is barely shrunk); an l1 coefficient shrunk to 0 means the penalty dropped it, not that the predictor is unrelated to the outcome.

#### `std_errors.*`

How much the coefficient would vary from sample to sample; smaller means a more precise estimate. It is the classic standard error, which assumes residuals of constant spread, unless the request's covariance names a robust one.

**Caveats:**

- Classic: with heteroscedasticity (residual spread that changes) it can be too small or too large, and with dependent rows it is usually too small; covariance hc3 guards the first, cluster (rows grouped by a field) or hac (a series in time) the second, and p_values, the intervals and vcov follow it.
- Null on every coefficient of an l1 or elasticnet fit: the penalty picks the predictors from the data, so no analytical standard error is valid (advisory PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED); request resample for replicate ones. An l2 fit's is the closed-form ridge sandwich.
- Null on every coefficient of a bounds fit whose non-negativity constraint binds (a coefficient held at 0): the constrained estimator has no classical standard error (advisory PULSE_ADVISORY_CONSTRAINT_ACTIVE); resample bootstrap refits the bounded model for replicate ones. When no bound binds the fit is plain OLS.

#### `p_values.*`

**Caveats:**

- The request's alpha param is the penalty strength for l1 / l2 / elasticnet fits, not a significance level; Pulse applies no significance threshold to these p-values.
- On an l2 fit the coefficients are shrunk on purpose, so these p-values do not have the usual reading; on an l1 or elasticnet fit they are null (no valid analytical form).

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) a Student-t critical value on df times its standard error.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Under resample the critical value is the normal one and the standard error the resampled one; df is then absent.
- On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) a Student-t critical value on df times its standard error.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Under resample the critical value is the normal one and the standard error the resampled one; df is then absent.
- On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `df`

Residual degrees of freedom the intervals' critical value reads: n - p - 1, with sum_weights or n_eff in place of n on a weighted fit, so it can be fractional; under covariance cluster, the number of clusters minus 1.

**Caveats:**

- Null when it is 0 or below: too few rows (or too small an effective sample) for the predictors, and every bound is then null too.

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

#### `two_log_lik`

Twice the maximised Gaussian log-likelihood (R's logLik on lm), at the residual variance RSS / n; higher is a closer fit.

**Caveats:**

- Absent on a probability-weighted fit (a weighted pseudo-likelihood is not a likelihood), a penalized fit, and an OLS fit whose bound binds.
- With a frequency weight it is the expanded rows' figure, which R's lm(weights = f) does not report (it reads them as precision weights).

#### `aic`

-two_log_lik + 2 x (coefficients + 1, the residual variance counted): R's AIC on lm; lower is better.

**Caveats:**

- Compare it only across models of the same outcome fitted to the same rows.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Coordinate-descent iterations an l1 or elasticnet fit took to converge; absent for fits solved in closed form.

**Caveats:**

- A value at max_iters suggests the fit stopped before settling; raise max_iters or tol.

#### `margins.estimates.*`

The marginal effect of this term (requested by margins): the change in the expected outcome per unit of the predictor, in outcome units. On OLS the model is linear, so every effect, ame or mem, response or link scale, equals the coefficient, with its standard error, p-value and interval.

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Each effect is a slope: the change for a small change in the predictor, other predictors held fixed. For a 0/1 predictor it is not the discrete difference between its two levels.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- A multiplicity block corrects these p-values as a family of their own, apart from the coefficients'.

#### `margins.std_errors.*`

The effect's delta-method standard error, on the fit's own coefficient covariance (classical, or the request's robust covariance).

#### `margins.p_values.*`

**Caveats:**

- The p-value and interval use the coefficient table's distribution (Student t on df where the coefficients read one, else normal).

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `collinearity.vif.*`

The variance inflation factor of this term (requested by collinearity): how many times larger its coefficient's variance is than it would be if the term were unrelated to the other predictors. 1 means no overlap. For a group of columns (a one-hot block's dummies) it is the generalized VIF, the same idea for the group's coefficients jointly.

**Caveats:**

- No convention bands it. Cut-offs of 5 or 10 are rules of thumb only (O'Brien 2007): a large sample can make a high value harmless, a small one a modest value matter.
- It depends on the predictors (and, on basis model of a GLM, the fitted weights), not on the outcome's values: it says how unstable the coefficients are, not whether a predictor matters.
- A group's generalized VIF grows with its number of columns; compare groups of different size through gvif_scaled, not vif.

#### `collinearity.tolerance.*`

The reciprocal of vif: the share of the term's spread the other predictors do not explain. Near 0 means the term is close to a combination of the others.

#### `collinearity.gvif_scaled.*`

vif^(1/(2 df)): the generalized VIF on the scale of a standard error, so terms of different column counts compare; for a single column it is the square root of vif.

<a id="op-reg_ordinal"></a>

### `REG_ORDINAL`

Fits a model that predicts an ordered rating (poor, fair, good) from numeric predictors, each shifting the whole scale up or down. See its [catalog entry](../catalog/regression.md#op-reg_ordinal).

#### `coefficients.*`

A predictor's coefficient is the shift of the latent scale for a one-unit increase in it, holding the other predictors fixed: under the logit link the log odds ratio of a higher rather than a lower level, the same at every step of the scale. A threshold `<level>|<next level>` (listed in outputs.thresholds) is the cut point zeta between two adjacent levels, in P(Y <= level | x) = F(zeta - x'beta).

**Sign:**

- `+`: higher values of the predictor are associated with higher levels of the outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with lower levels of the outcome, other predictors held fixed

**Caveats:**

- Raised to the power e, a logit coefficient is an odds ratio per one-unit increase, of being above any given level; under probit it shifts a normal latent score by that many standard deviations, under cloglog it is a log hazard ratio.
- It assumes the effect is the same at every step (proportional odds); params.brant tests it.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- Its size depends on the predictor's units, so do not rank predictors by raw coefficients unless they share units; standardize the predictors first to compare them.
- With strongly related predictors (multicollinearity) a coefficient can swing, or even flip sign, between fits on similar data.
- A threshold is not a predictor's effect: it only places the scale's cut points, and moves with the predictors' origin.

#### `std_errors.*`

How much the coefficient or threshold would vary from sample to sample, from the inverse of the fitted (observed) information matrix.

**Caveats:**

- Unless the request's covariance names a robust one: hc0 or hc1 (the sandwich over each row's score) or cluster (rows grouped by a field); p_values, the intervals and vcov follow it.

#### `statistics.*`

The Wald statistic: the coefficient or threshold divided by its standard error.

**Caveats:**

- A threshold's statistic tests a cut point of 0, which only says where the scale sits when every predictor is 0; read it as precision, not as evidence.

#### `p_values.*`

**Caveats:**

- A Wald z on the fitted information (or the robust covariance). Thresholds carry none (null): a cut point of 0 is no hypothesis.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `ci_lower.*`

Lower bound of the coefficient's two-sided confidence interval at conf_level: the coefficient or threshold minus (lower) or plus (upper) a standard normal critical value times its standard error (a Wald interval).

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Raised to the power e, the bounds of a logit coefficient bound its odds ratio.

#### `ci_upper.*`

Upper bound of the coefficient's two-sided confidence interval at conf_level: the coefficient or threshold minus (lower) or plus (upper) a standard normal critical value times its standard error (a Wald interval).

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Raised to the power e, the bounds of a logit coefficient bound its odds ratio.

#### `conf_level`

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

#### `deviance`

-2 x the maximised log-likelihood (MASS polr's deviance): lower is a closer fit; compare it only with null_deviance or with another model fitted to the same rows and outcome.

#### `null_deviance`

The deviance of the thresholds-only model, which predicts every row at the overall share of each level: the baseline the predictors are judged against.

#### `pseudo_r2`

1 - deviance / null_deviance, McFadden's pseudo-R-squared: the share of the baseline's deviance the predictors remove, from 0 to 1.

**Caveats:**

- It is not a share of variance explained and runs much lower than an OLS R-squared for an equally useful model; there are no published bands for it, so none are attached.

#### `two_log_lik`

Twice the maximised log-likelihood (R's logLik on polr); higher is a closer fit.

**Caveats:**

- Absent on a probability-weighted fit (a weighted pseudo-likelihood is not a likelihood).

#### `aic`

-two_log_lik + 2 x the parameter count (the coefficients and the thresholds; R's AIC on polr); lower is better.

**Caveats:**

- Compare it only across models of the same outcome fitted to the same rows.

#### `n_obs`

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

#### `converged_iters`

Newton-Raphson steps taken before the coefficients and thresholds stopped changing.

**Caveats:**

- A fit that cannot converge (a predictor that separates the levels) fails its slot alone rather than being reported.

#### `outputs.*`

outputs.levels and outputs.thresholds name the fitted order and the threshold coefficients; outputs.link the link; outputs.fitted_probabilities the mean fitted probability of each level over the fitted rows; outputs.brant, with params.brant, the Brant test: a chi-square omnibus and one per predictor of the effect being the same at every step, whose small p-value says the proportional-odds assumption fails for it.

**Caveats:**

- The Brant test reads K - 1 separate binary logits, so it needs enough rows at every level; a sparse level makes it unreliable, and a split a predictor separates leaves its figures null.
- A non-significant Brant test does not prove the assumption holds; with many rows a trivial difference is significant.
