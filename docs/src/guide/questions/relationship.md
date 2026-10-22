# Relationship between measures

## The question

<!-- docgen:preserve begin guide-relationship-question -->
You have two measures and want to know whether they go together: when one is high, does the other tend to be high (or low) too, and how tightly? For two numeric fields that is a correlation; for two categorical fields it is a test of whether the categories are independent, read from a crosstab of the two.

It sounds like:

- "Do customers who spent more last year spend more this year?"
- "Is age associated with income?"
- "Does conversion depend on the customer segment?"

A relationship is symmetric: nothing is cast as the cause and nothing as the effect. When you want to say how much one outcome moves with several measures at once, that is the drivers question.
<!-- docgen:preserve end guide-relationship-question -->

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

**Do customers who spent more before the test also spend more during it?**

The request (example `pearson_correlation`; `pulse examples show pearson_correlation` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_COUNT",
      "field": "revenue",
      "label": "n"
    }
  ],
  "cohort": {
    "filename": "experiment.pulse",
    "data_dir": ".data"
  },
  "return": {
    "precision": 4
  },
  "tests": [
    {
      "type": "TEST_PEARSON_R",
      "field": "revenue",
      "field2": "revenue_before",
      "alpha": 0.05,
      "label": "revenue_pre_post_pearson"
    }
  ]
}
```

The result:

```json
{
  "data": [
    {
      "n": 400
    }
  ],
  "metadata": {
    "total_rows": 400,
    "filtered_rows": 400,
    "cohort_file": "experiment.pulse"
  },
  "tests": [
    {
      "label": "revenue_pre_post_pearson",
      "type": "TEST_PEARSON_R",
      "variant": "pearson",
      "statistic": 0.9777,
      "df": 398,
      "p_value": 6.718e-272,
      "alpha": 0.05,
      "reject_null": true,
      "details": {
        "ci_high": 0.9817,
        "ci_low": 0.9729,
        "covariance": 6909,
        "mean_x": 154.3,
        "mean_y": 123.4,
        "n": 400,
        "t": 92.9,
        "variance_x": 8825,
        "variance_y": 5658
      }
    }
  ],
  "components": {
    "aggregations": [
      {
        "label": "n",
        "n": 400,
        "n_null": 0
      }
    ],
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

Very strongly. The correlation sits close to its ceiling and its interval is narrow, so customers keep almost exactly their spending order from before the test to during it. A correlation this high says the two measures rise and fall together; on its own it says nothing about cause, and nothing about whether spending went up overall, which is a before-and-after comparison of the same customers.

## How to read the result

### `TEST_PEARSON_R`

**`statistic`**

r measures how closely the two fields follow a straight line together, from -1 to +1; 0 means no straight-line link.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.1 | very small |
| from 0.1 to below 0.3 | small |
| from 0.3 to below 0.5 | medium |
| 0.5 and above | large |

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- An r near zero rules out only a straight-line link; a curved relationship can still be strong.
- A few extreme rows, or a narrow range of either field, can move r a lot.

**`p_value`**

Follows the shared reading: see [Reading a p-value](../reading/test.md#shared-p-value).

**`details.ci_low`**

Lower end of the confidence interval for r at the 1 - alpha level (95% by default).

**Caveats:**

- With fewer than four pairs (total weight, under frequency weights), or a perfect r, no interval can be computed and both ends are set to r: this is not a precise estimate. Treat r as highly uncertain and do not report the interval.

**`details.ci_high`**

Upper end of the confidence interval for r at the 1 - alpha level (95% by default).

More on reading it: [TEST_PEARSON_R](../reading/test.md#op-test_pearson_r).

## Pitfalls

<!-- docgen:preserve begin guide-relationship-pitfalls -->
- **Association is not causation.** Two measures can move together because a third thing drives both, or by the way the rows were selected; a correlation never says which way, if any, the influence runs.
- **Pearson's r sees straight lines only.** A strong curved or step-shaped relationship can show a weak r; plot the data or use a rank correlation (Spearman, Kendall), which also resists a few extreme values.
- **One or two outliers can make or break a correlation.** Check whether the figure survives the rank-based version before reporting it.
- **A significant correlation can be tiny.** With many rows a correlation close to zero still clears the bar; read the size of r and its interval, not only the p-value.
- **Chi-square needs enough rows per cell.** When some expected cell counts are small, use Fisher's exact test instead; a crosstab with many sparse cells is better collapsed into fewer categories first.
- **Group averages can tell a different story from rows.** A relationship between group means (region by region) need not hold for the individual rows inside them; the ecological fallacy example shows the reversal.
- **Many pairs at once inflate false alarms.** A full correlation table across many fields will show some strong-looking pairs by luck; treat them as leads to confirm, not findings.
<!-- docgen:preserve end guide-relationship-pitfalls -->

## Next steps

**Related questions:**

- [Find drivers](drivers.md)
- [Compare groups](compare_groups.md)
- [Describe a measure](describe.md)

**Skills:** [`intents`](../skills/intents.md), [`attribute-composition`](../skills/attribute-composition.md), [`feature-engineering`](../skills/feature-engineering.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`regression-modeling`](../skills/regression-modeling.md), [`statistical-testing`](../skills/statistical-testing.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-test-pearson-r`](../skills/op-test-pearson-r.md)

**From the command line:**

- `pulse recommend --intent relationship --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
