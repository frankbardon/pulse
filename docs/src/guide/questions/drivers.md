# Find drivers

## The question

<!-- docgen:preserve begin guide-drivers-question -->
You have one outcome — revenue, churn, a satisfaction score — and several candidate factors, and you want to know which of them explain it, how much each one moves the outcome when the others are held fixed, and how much of the outcome the factors explain together. This is regression: a numeric outcome takes a linear model, a yes/no outcome a logistic one.

It sounds like:

- "What explains a customer's revenue?"
- "Which factors matter most for churn?"
- "Does session time still matter once we account for past spending?"

Read three things from a fit: each coefficient (how much the outcome moves per unit of that factor, others held fixed), its interval and p-value (whether the data pins that movement down), and R-squared or the model's fit score (how much of the outcome the factors explain together).
<!-- docgen:preserve end guide-drivers-question -->

## Pick your analysis

This question fits data shaped like:

- `outcome`: one numeric, categorical or bool field; `predictors`: 1 or more numeric, categorical or bool fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_AUC`](../catalog/aggregator.md#op-agg_auc) | How well a score separates a yes/no outcome: the chance that a randomly picked yes row scores higher than a randomly picked no row. | intermediate |
| [`AGG_MAE`](../catalog/aggregator.md#op-agg_mae) | Average size of a prediction's miss in the actual's own units, comparing an actual column with a prediction column row by row. | intermediate |
| [`AGG_MAPE`](../catalog/aggregator.md#op-agg_mape) | Average size of a prediction's miss as a share of the actual value, comparing an actual column with a prediction column row by row. | intermediate |
| [`AGG_RMSE`](../catalog/aggregator.md#op-agg_rmse) | Typical size of a prediction's miss in the actual's own units, with large misses counting more: the root of the average squared miss. | intermediate |

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_REG_PREDICT`](../catalog/attribute.md#op-attr_reg_predict) | Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values. | advanced |

### Regressions

| Use | In plain words | Level |
|---|---|---|
| [`REG_OLS`](../catalog/regression.md#op-reg_ols) | Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares. | intermediate |
| [`REG_BAYES_LINEAR`](../catalog/regression.md#op-reg_bayes_linear) | Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval. | advanced |
| [`REG_GLM`](../catalog/regression.md#op-reg_glm) | Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts. | advanced |
| [`REG_MMM_BAYES`](../catalog/regression.md#op-reg_mmm_bayes) | Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls. | advanced |
| [`REG_MMM_HILL`](../catalog/regression.md#op-reg_mmm_hill) | Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls. | advanced |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_COLLINEARITY`](../catalog/matrix.md#op-mat_collinearity) | Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled. | intermediate |
| [`MAT_PARTIAL_CORRELATION`](../catalog/matrix.md#op-mat_partial_correlation) | How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. | advanced |

## A worked example

**What explains a customer's revenue: what they spent before, or how long their sessions run?**

The request (example `clustered_covariance`; `pulse examples show clustered_covariance` prints it):

```json
{
  "cohort": {
    "filename": "experiment.pulse",
    "data_dir": ".data"
  },
  "regressions": [
    {
      "type": "REG_OLS",
      "name": "revenue_clustered_by_region",
      "target": "revenue",
      "predictors": [
        "revenue_before",
        "session_minutes"
      ],
      "covariance": {
        "type": "cluster",
        "cluster": "region"
      }
    }
  ],
  "return": {
    "precision": 4
  }
}
```

The result:

```json
{
  "metadata": {
    "total_rows": 400,
    "filtered_rows": 400,
    "cohort_file": "experiment.pulse"
  },
  "regressions": [
    {
      "name": "revenue_clustered_by_region",
      "type": "REG_OLS",
      "coefficients": {
        "(intercept)": -0.02415,
        "revenue_before": 1.224,
        "session_minutes": 0.2382
      },
      "std_errors": {
        "(intercept)": 1.787,
        "revenue_before": 0.007101,
        "session_minutes": 0.1366
      },
      "p_values": {
        "(intercept)": 0.9901,
        "revenue_before": 4.301e-07,
        "session_minutes": 0.1796
      },
      "ci_lower": {
        "(intercept)": -5.71,
        "revenue_before": 1.202,
        "session_minutes": -0.1966
      },
      "ci_upper": {
        "(intercept)": 5.661,
        "revenue_before": 1.247,
        "session_minutes": 0.673
      },
      "df": 3,
      "conf_level": 0.95,
      "r2": 0.9566,
      "adj_r2": 0.9564,
      "two_log_lik": -3513,
      "aic": 3521,
      "n_obs": 400,
      "residual_std_err": 19.61,
      "covariance": {
        "type": "cluster",
        "cluster": "region",
        "clusters": 4
      }
    }
  ],
  "components": {
    "run": {
      "total_records": 400,
      "filtered_records": 400,
      "null_records": 0
    }
  },
  "returned": {
    "preset": "full",
    "digest": "rp1:2a47356146fe7626329ca77359f19495ac57bb6a599dfdd7658729be64597be6",
    "precision": 4
  }
}
```

Earlier spending explains nearly all of it. R-squared is close to one, and each extra unit of earlier revenue goes with somewhat more than one extra unit now, an estimate pinned down tightly. Session time adds nothing that can be told apart from noise once earlier spending is in the model: its interval spans zero. The standard errors treat customers in the same region as a cluster that may share conditions; with only a handful of regions that check is deliberately cautious. A coefficient describes how revenue moves with a predictor in this data, not what would happen if you changed that predictor.

## How to read the result

### `REG_OLS`

**`coefficients.*`**

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

**`std_errors.*`**

How much the coefficient would vary from sample to sample; smaller means a more precise estimate. It is the classic standard error, which assumes residuals of constant spread, unless the request's covariance names a robust one.

**Caveats:**

- Classic: with heteroscedasticity (residual spread that changes) it can be too small or too large, and with dependent rows it is usually too small; covariance hc3 guards the first, cluster (rows grouped by a field) or hac (a series in time) the second, and p_values, the intervals and vcov follow it.
- Null on every coefficient of an l1 or elasticnet fit: the penalty picks the predictors from the data, so no analytical standard error is valid (advisory PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED); request resample for replicate ones. An l2 fit's is the closed-form ridge sandwich.
- Null on every coefficient of a bounds fit whose non-negativity constraint binds (a coefficient held at 0): the constrained estimator has no classical standard error (advisory PULSE_ADVISORY_CONSTRAINT_ACTIVE); resample bootstrap refits the bounded model for replicate ones. When no bound binds the fit is plain OLS.

**`p_values.*`**

**Caveats:**

- The request's alpha param is the penalty strength for l1 / l2 / elasticnet fits, not a significance level; Pulse applies no significance threshold to these p-values.
- On an l2 fit the coefficients are shrunk on purpose, so these p-values do not have the usual reading; on an l1 or elasticnet fit they are null (no valid analytical form).

Follows the shared reading: see [Reading a p-value](../reading/test.md#shared-p-value).

**`ci_lower.*`**

Lower bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) a Student-t critical value on df times its standard error.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Under resample the critical value is the normal one and the standard error the resampled one; df is then absent.
- On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.

**`ci_upper.*`**

Upper bound of the coefficient's two-sided confidence interval at conf_level: the coefficient minus (lower) or plus (upper) a Student-t critical value on df times its standard error.

**Caveats:**

- It is a statement about the method, not this one range: intervals built this way cover the true coefficient in about conf_level of repeated samples, if the model's assumptions hold.
- Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level.
- Under resample the critical value is the normal one and the standard error the resampled one; df is then absent.
- On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.

**`conf_level`**

The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

**`df`**

Residual degrees of freedom the intervals' critical value reads: n - p - 1, with sum_weights or n_eff in place of n on a weighted fit, so it can be fractional; under covariance cluster, the number of clusters minus 1.

**Caveats:**

- Null when it is 0 or below: too few rows (or too small an effective sample) for the predictors, and every bound is then null too.

**`r2`**

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

**`adj_r2`**

R-squared corrected for the number of predictors: 1 - (1 - r2) * (n - 1) / (n - p - 1), for p predictors.

**Caveats:**

- It can fall below 0 when the predictors fit no better than chance would.
- There are no published bands for adjusted R-squared, so none are attached: Cohen's R-squared benchmarks are stated for R-squared itself.

**`residual_std_err`**

Typical size of a residual, in the outcome's units: how far actual values sit from the fitted line on a typical row. It divides the residual sum of squares by n - p - 1.

**`two_log_lik`**

Twice the maximised Gaussian log-likelihood (R's logLik on lm), at the residual variance RSS / n; higher is a closer fit.

**Caveats:**

- Absent on a probability-weighted fit (a weighted pseudo-likelihood is not a likelihood), a penalized fit, and an OLS fit whose bound binds.
- With a frequency weight it is the expanded rows' figure, which R's lm(weights = f) does not report (it reads them as precision weights).

**`aic`**

-two_log_lik + 2 x (coefficients + 1, the residual variance counted): R's AIC on lm; lower is better.

**Caveats:**

- Compare it only across models of the same outcome fitted to the same rows.

**`n_obs`**

Rows used in the fit, after rows with a null target or predictor were dropped.

**Caveats:**

- Few rows per predictor make every estimate unstable and invite overfitting.
- Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.

**`converged_iters`**

Coordinate-descent iterations an l1 or elasticnet fit took to converge; absent for fits solved in closed form.

**Caveats:**

- A value at max_iters suggests the fit stopped before settling; raise max_iters or tol.

**`margins.estimates.*`**

The marginal effect of this term (requested by margins): the change in the expected outcome per unit of the predictor, in outcome units. On OLS the model is linear, so every effect, ame or mem, response or link scale, equals the coefficient, with its standard error, p-value and interval.

**Sign:**

- `+`: higher values of the predictor are associated with a higher outcome, other predictors held fixed
- `-`: higher values of the predictor are associated with a lower outcome, other predictors held fixed

**Caveats:**

- Each effect is a slope: the change for a small change in the predictor, other predictors held fixed. For a 0/1 predictor it is not the discrete difference between its two levels.
- Association, not causation: the coefficient shows how the outcome moves with this predictor in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, and a predictor left out of the model can drive both.
- A multiplicity block corrects these p-values as a family of their own, apart from the coefficients'.

**`margins.std_errors.*`**

The effect's delta-method standard error, on the fit's own coefficient covariance (classical, or the request's robust covariance).

**`margins.p_values.*`**

**Caveats:**

- The p-value and interval use the coefficient table's distribution (Student t on df where the coefficients read one, else normal).

Follows the shared reading: see [Reading a p-value](../reading/test.md#shared-p-value).

**`collinearity.vif.*`**

The variance inflation factor of this term (requested by collinearity): how many times larger its coefficient's variance is than it would be if the term were unrelated to the other predictors. 1 means no overlap. For a group of columns (a one-hot block's dummies) it is the generalized VIF, the same idea for the group's coefficients jointly.

**Caveats:**

- No convention bands it. Cut-offs of 5 or 10 are rules of thumb only (O'Brien 2007): a large sample can make a high value harmless, a small one a modest value matter.
- It depends on the predictors (and, on basis model of a GLM, the fitted weights), not on the outcome's values: it says how unstable the coefficients are, not whether a predictor matters.
- A group's generalized VIF grows with its number of columns; compare groups of different size through gvif_scaled, not vif.

**`collinearity.tolerance.*`**

The reciprocal of vif: the share of the term's spread the other predictors do not explain. Near 0 means the term is close to a combination of the others.

**`collinearity.gvif_scaled.*`**

vif^(1/(2 df)): the generalized VIF on the scale of a standard error, so terms of different column counts compare; for a single column it is the square root of vif.

More on reading it: [REG_OLS](../reading/regression.md#op-reg_ols).

## Pitfalls

<!-- docgen:preserve begin guide-drivers-pitfalls -->
- **A driver in the model is not a cause in the world.** A coefficient says how the outcome moves with a factor in this data; it is not what would happen if you changed that factor, unless the data come from an experiment that set it.
- **Correlated factors share the credit.** When two predictors move together, the model cannot tell them apart: coefficients swing, intervals widen and a factor can look unimportant only because its twin is in the model; check collinearity before ranking factors.
- **Coefficients in different units are not comparable.** A larger coefficient is not a stronger driver when one factor is in dollars and another in minutes; compare standardized figures or the change across a typical range.
- **A high R-squared can be trivial.** An outcome explained by its own earlier value (last month's revenue predicting this month's) fits well and says little; ask what the model adds beyond the obvious.
- **Rows that share conditions are not independent.** Customers in the same region, or repeated rows per person, make standard errors too small; use a clustered or robust covariance, and expect wider intervals when there are only a few clusters.
- **Too many factors for the rows overfits.** With few rows per predictor the fit chases noise; prefer fewer factors, a penalized fit or selection, and score the model on held-out rows.
- **A missing value drops the whole row.** Every predictor's nulls remove the row from the fit, so the model can quietly run on far fewer rows than the cohort; check the number of observations it reports.
<!-- docgen:preserve end guide-drivers-pitfalls -->

## Next steps

**Related questions:**

- [Relationship between measures](relationship.md)
- [Benchmark against a reference](benchmark.md)
- [Data quality](data_quality.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`regression-modeling`](../skills/regression-modeling.md), [`op-reg-ols`](../skills/op-reg-ols.md)

**From the command line:**

- `pulse recommend --intent drivers --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
