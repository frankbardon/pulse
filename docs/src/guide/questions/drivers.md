# Find drivers

## The question

Questions of this kind sound like:

- What explains Y?
- Which factors matter most for Y?

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

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_COLLINEARITY`](../catalog/matrix.md#op-mat_collinearity) | Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled. | intermediate |
| [`MAT_PARTIAL_CORRELATION`](../catalog/matrix.md#op-mat_partial_correlation) | How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent drivers` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Relationship between measures](relationship.md)
- [Benchmark against a reference](benchmark.md)
- [Data quality](data_quality.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`regression-modeling`](../skills/regression-modeling.md)

**From the command line:**

- `pulse recommend --intent drivers --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
