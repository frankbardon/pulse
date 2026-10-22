# Relationship between measures

## The question

Questions of this kind sound like:

- Do X and Y move together?
- Is X associated with Y?

## Pick your analysis

This question fits data shaped like:

- `measures`: 2 or more numeric fields
- `categories`: 2 categorical or bool fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_REG_FITTED`](../catalog/attribute.md#op-attr_reg_fitted) | Adds to every row the value a straight-line model predicts for its target from its predictors. | advanced |
| [`ATTR_REG_PREDICT`](../catalog/attribute.md#op-attr_reg_predict) | Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values. | advanced |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_POLY`](../catalog/feature.md#op-feat_poly) | Adds columns holding the value squared, cubed and so on up to a chosen power, so a straight-line model can bend into a curve. | advanced |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_CHISQ`](../catalog/test.md#op-test_chisq) | Checks whether two categorical fields are associated, or whether one categorical field's counts match planned proportions. | intermediate |
| [`TEST_FISHER_EXACT`](../catalog/test.md#op-test_fisher_exact) | Exact test of whether two yes/no fields are associated, built for small 2x2 tables. | intermediate |
| [`TEST_KENDALL_TAU`](../catalog/test.md#op-test_kendall_tau) | Measures how often pairs of rows agree in order on two numeric fields: a rank-based link suited to small samples and ties. | intermediate |
| [`TEST_PEARSON_R`](../catalog/test.md#op-test_pearson_r) | Measures how strongly two numeric fields rise and fall together along a straight line. | intermediate |
| [`TEST_SPEARMAN_R`](../catalog/test.md#op-test_spearman_r) | Measures how consistently one numeric field rises (or falls) as the other rises, using ranks, so the link need not be a straight line. | intermediate |
| [`TEST_BARTLETT_SPHERICITY`](../catalog/test.md#op-test_bartlett_sphericity) | Checks whether a set of numeric fields are related at all, the first check before summarising them as a few underlying dimensions. | advanced |

### Regressions

| Use | In plain words | Level |
|---|---|---|
| [`REG_OLS`](../catalog/regression.md#op-reg_ols) | Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares. | intermediate |
| [`REG_BAYES_LINEAR`](../catalog/regression.md#op-reg_bayes_linear) | Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval. | advanced |
| [`REG_GLM`](../catalog/regression.md#op-reg_glm) | Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts. | advanced |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_CORRELATION`](../catalog/matrix.md#op-mat_correlation) | How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1. | intermediate |
| [`MAT_COVARIANCE`](../catalog/matrix.md#op-mat_covariance) | How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal. | intermediate |
| [`MAT_PARTIAL_CORRELATION`](../catalog/matrix.md#op-mat_partial_correlation) | How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_CHISQ_MATRIX`](../catalog/overlay.md#op-overlay_chisq_matrix) | Chi-square test on a whole crosstab: checks whether the row and column categories are associated, with one p-value for the table. | intermediate |
| [`OVERLAY_MATRIX_GROUP_SUMMARY`](../catalog/overlay.md#op-overlay_matrix_group_summary) | Summarize a grouped matrix across its groups: each cell's mean, minimum, maximum and range, and how many groups fed it. | intermediate |
| [`OVERLAY_CORRESPONDENCE`](../catalog/overlay.md#op-overlay_correspondence) | Correspondence analysis of a crosstab: a perceptual map placing rows and columns in a few dimensions so similar profiles sit close together. | advanced |
| [`OVERLAY_STD_RESIDUAL`](../catalog/overlay.md#op-overlay_std_residual) | Adjusted residual for every crosstab cell: how far its count sits above or below what its row and column totals predict. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent relationship` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Find drivers](drivers.md)
- [Compare groups](compare_groups.md)
- [Describe a measure](describe.md)

**Skills:** [`intents`](../skills/intents.md), [`attribute-composition`](../skills/attribute-composition.md), [`feature-engineering`](../skills/feature-engineering.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`regression-modeling`](../skills/regression-modeling.md), [`statistical-testing`](../skills/statistical-testing.md)

**From the command line:**

- `pulse recommend --intent relationship --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
