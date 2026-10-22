# Describe a measure

## The question

<!-- docgen:preserve begin guide-describe-question -->
You have a measure — income, order value, a satisfaction score, a set of ticked options — and you want to know what it looks like across the rows: its typical value, its total, how spread out it is, and how precisely the data pins those figures down. Nothing is being compared yet; this is the summary you read before any comparison.

It sounds like:

- "What's the typical order value?"
- "What did customers spend in total last quarter?"
- "How much do satisfaction scores vary, and how sure are we of the average?"
<!-- docgen:preserve end guide-describe-question -->

## Pick your analysis

This question fits data shaped like:

- `measure`: 1 or more numeric fields
- `measure`: 1 or more categorical, bool or set fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_AVERAGE`](../catalog/aggregator.md#op-agg_average) | Average of a numeric field, over all rows or per group. | basic |
| [`AGG_COUNT`](../catalog/aggregator.md#op-agg_count) | Number of rows that have a value in a field, over all rows or per group. | basic |
| [`AGG_DISTINCT_COUNT`](../catalog/aggregator.md#op-agg_distinct_count) | Number of different values a field takes, over all rows or per group. | basic |
| [`AGG_FREQUENCY`](../catalog/aggregator.md#op-agg_frequency) | How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group. | basic |
| [`AGG_MAX`](../catalog/aggregator.md#op-agg_max) | Largest value of a numeric or date field, over all rows or per group. | basic |
| [`AGG_MEDIAN`](../catalog/aggregator.md#op-agg_median) | Middle value of a numeric field once sorted: the typical row, not pulled by a few extreme values. | basic |
| [`AGG_MIN`](../catalog/aggregator.md#op-agg_min) | Smallest value of a numeric or date field, over all rows or per group. | basic |
| [`AGG_MODE`](../catalog/aggregator.md#op-agg_mode) | Most common value of a field, over all rows or per group. | basic |
| [`AGG_MODE_COUNT`](../catalog/aggregator.md#op-agg_mode_count) | How many rows share a field's most common value, over all rows or per group. | basic |
| [`AGG_PERCENTILE`](../catalog/aggregator.md#op-agg_percentile) | Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time. | basic |
| [`AGG_RANGE`](../catalog/aggregator.md#op-agg_range) | Distance between the smallest and largest value of a numeric field, over all rows or per group. | basic |
| [`AGG_RATIO`](../catalog/aggregator.md#op-agg_ratio) | Total of one field divided by the total of another, over all rows or per group, such as revenue per order. | basic |
| [`AGG_SET_CARDINALITY_AVG`](../catalog/aggregator.md#op-agg_set_cardinality_avg) | For a multi-select field, the average number of options chosen per row. | basic |
| [`AGG_SET_CARDINALITY_SUM`](../catalog/aggregator.md#op-agg_set_cardinality_sum) | For a multi-select field, the total number of options chosen across all rows. | basic |
| [`AGG_SET_DISTINCT_VALUES`](../catalog/aggregator.md#op-agg_set_distinct_values) | For a multi-select field, how many different combinations of options appear. | basic |
| [`AGG_SET_FREQUENCY`](../catalog/aggregator.md#op-agg_set_frequency) | For a multi-select field, how many rows chose each option, over all rows or per group. | basic |
| [`AGG_SET_INTERSECTION`](../catalog/aggregator.md#op-agg_set_intersection) | For a multi-select field, the options chosen by every row, over all rows or per group. | basic |
| [`AGG_SET_UNION`](../catalog/aggregator.md#op-agg_set_union) | For a multi-select field, every option chosen by at least one row, over all rows or per group. | basic |
| [`AGG_STDDEV`](../catalog/aggregator.md#op-agg_stddev) | Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group. | basic |
| [`AGG_SUM`](../catalog/aggregator.md#op-agg_sum) | Total of a numeric field, over all rows or per group. | basic |
| [`AGG_CI_LOWER`](../catalog/aggregator.md#op-agg_ci_lower) | Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. | intermediate |
| [`AGG_CI_UPPER`](../catalog/aggregator.md#op-agg_ci_upper) | Upper end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. | intermediate |
| [`AGG_DISTINCT_SUM`](../catalog/aggregator.md#op-agg_distinct_sum) | Total of a numeric field counting each key once, such as a respondent's weight summed once per respondent. | intermediate |
| [`AGG_VARIANCE`](../catalog/aggregator.md#op-agg_variance) | Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group. | intermediate |
| [`AGG_VEC_MEAN`](../catalog/aggregator.md#op-agg_vec_mean) | Average of each item of a battery or mix, as one list per group: the group's average profile across the items. | intermediate |
| [`AGG_VEC_SD`](../catalog/aggregator.md#op-agg_vec_sd) | Standard deviation of each item of a battery or mix, as one list per group: how much the group's rows disagree on each item. | intermediate |
| [`AGG_VEC_SUM`](../catalog/aggregator.md#op-agg_vec_sum) | Total of each item of a set of related fields, as one list per group. | intermediate |
| [`AGG_WEIGHTED_MEAN`](../catalog/aggregator.md#op-agg_weighted_mean) | Average of a numeric field where each row counts in proportion to a weight field, such as a survey weight. | intermediate |
| [`AGG_WELFORD`](../catalog/aggregator.md#op-agg_welford) | Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs. | intermediate |
| [`AGG_ZSCORE`](../catalog/aggregator.md#op-agg_zscore) | Population mean and standard deviation of a numeric field, for standardizing it; the value itself is always 0. | intermediate |

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_SET_POPCOUNT`](../catalog/attribute.md#op-attr_set_popcount) | Adds to every row the number of options its multi-select field has selected. | basic |

### Window operators

| Use | In plain words | Level |
|---|---|---|
| [`WIN_RUNNING_AVG`](../catalog/window.md#op-win_running_avg) | Adds the average of a field over the rows so far in the ordered series, such as the average order value to date. | basic |
| [`WIN_RUNNING_SUM`](../catalog/window.md#op-win_running_sum) | Adds a running total of a field down the ordered rows, such as revenue for the year to date. | basic |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_CORRELATION`](../catalog/matrix.md#op-mat_correlation) | How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1. | intermediate |
| [`MAT_COVARIANCE`](../catalog/matrix.md#op-mat_covariance) | How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal. | intermediate |
| [`MAT_PCA`](../catalog/matrix.md#op-mat_pca) | How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_RANK`](../catalog/overlay.md#op-overlay_rank) | Ranks each cell of a target Compose request's crosstab (1 = largest) within its row, its column or the whole table. | basic |
| [`OVERLAY_FORMULA`](../catalog/overlay.md#op-overlay_formula) | Computes a custom figure for every cell, group or total from an expression over the value and its margins, totals or prior point. | advanced |

## A worked example

**What is a typical customer's income, and how precisely does this cohort pin it down?**

The request (example `confidence_interval_normal`; `pulse examples show confidence_interval_normal` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_AVERAGE",
      "field": "income",
      "label": "mean_income"
    },
    {
      "type": "AGG_CI_LOWER",
      "field": "income",
      "label": "ci_lower_95",
      "params": {
        "confidence": 0.95,
        "method": "normal"
      }
    },
    {
      "type": "AGG_CI_UPPER",
      "field": "income",
      "label": "ci_upper_95",
      "params": {
        "confidence": 0.95,
        "method": "normal"
      }
    }
  ],
  "cohort": {
    "filename": "customers.pulse",
    "data_dir": ".data"
  },
  "return": {
    "precision": 4
  }
}
```

The result:

```json
{
  "data": [
    {
      "ci_lower_95": 4.356e+04,
      "ci_upper_95": 5.252e+04,
      "mean_income": 4.804e+04
    }
  ],
  "metadata": {
    "total_rows": 200,
    "filtered_rows": 200,
    "cohort_file": "customers.pulse"
  },
  "components": {
    "aggregations": [
      {
        "label": "mean_income",
        "n": 200,
        "n_null": 0,
        "operator": {
          "sum": 9.608e+06
        }
      },
      {
        "label": "ci_lower_95",
        "n": 200,
        "n_null": 0,
        "operator": {
          "alpha": 0.05,
          "lower": 4.356e+04,
          "mean": 4.804e+04,
          "stderr": 2286,
          "t_critical": 1.96
        }
      },
      {
        "label": "ci_upper_95",
        "n": 200,
        "n_null": 0,
        "operator": {
          "alpha": 0.05,
          "mean": 4.804e+04,
          "stderr": 2286,
          "t_critical": 1.96,
          "upper": 5.252e+04
        }
      }
    ],
    "run": {
      "total_records": 200,
      "filtered_records": 200,
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

The mean income sits in the middle of a fairly tight interval: each bound lies within about a tenth of the mean, so the cohort pins the typical income down well. The interval describes the mean, not individual customers, whose incomes spread far more widely.

## How to read the result

### `AGG_CI_LOWER`

**`value`**

The lower end of a confidence interval for the group's mean at the requested confidence (95% by default): mean - z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).

**Caveats:**

- The interval is about the MEAN, not about individual rows: most rows can, and usually do, fall outside it.
- The confidence level describes the method: over repeated samples, intervals built this way capture the true mean that share of the time. Any one interval either contains it or not.
- Pulse uses the normal critical value (1.96 at 95%), not Student's t, so on small groups (under about 30 rows) the interval is too narrow; on a strongly skewed or heavy-tailed field it can stay too narrow, and lopsided, on much larger groups, so check AGG_SKEWNESS first. The components key t_critical holds that normal value.
- It assumes rows are independent draws. A row weight is honoured, its sample size read as the sum of weights (frequency) or Kish's effective sample size (probability, components n_eff); clustered or repeated rows, or design-based (strata / cluster) variance, are not modelled, so the interval understates the uncertainty there.
- Overlapping intervals do not show two means are equal: means can differ even when their intervals overlap, so compare groups with TEST_WELCH, not by eye.
- Empty (NaN) when fewer than two rows have a value (weighted: when the sample size is 1 or less).

More on reading it: [AGG_CI_LOWER](../reading/descriptive.md#op-agg_ci_lower).

### `AGG_CI_UPPER`

**`value`**

The upper end of a confidence interval for the group's mean at the requested confidence (95% by default): mean + z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).

**Caveats:**

- The interval is about the MEAN, not about individual rows: most rows can, and usually do, fall outside it.
- The confidence level describes the method: over repeated samples, intervals built this way capture the true mean that share of the time. Any one interval either contains it or not.
- Pulse uses the normal critical value (1.96 at 95%), not Student's t, so on small groups (under about 30 rows) the interval is too narrow; on a strongly skewed or heavy-tailed field it can stay too narrow, and lopsided, on much larger groups, so check AGG_SKEWNESS first. The components key t_critical holds that normal value.
- It assumes rows are independent draws. A row weight is honoured, its sample size read as the sum of weights (frequency) or Kish's effective sample size (probability, components n_eff); clustered or repeated rows, or design-based (strata / cluster) variance, are not modelled, so the interval understates the uncertainty there.
- Overlapping intervals do not show two means are equal: means can differ even when their intervals overlap, so compare groups with TEST_WELCH, not by eye.
- Empty (NaN) when fewer than two rows have a value (weighted: when the sample size is 1 or less).

More on reading it: [AGG_CI_UPPER](../reading/descriptive.md#op-agg_ci_upper).

## Pitfalls

<!-- docgen:preserve begin guide-describe-pitfalls -->
- **The average is not always typical.** A few very large values pull the mean up; when the measure is skewed (incomes, order values, durations), read the median beside it and say which one you report.
- **Missing values are left out, not counted as zero.** Every aggregate reports how many rows it used (`n`) and how many were null (`n_null`) in its components; check them before comparing two figures.
- **A total depends on how many rows there are.** Filter the cohort or change the period and the total moves even when nothing about the typical row changed; use an average or a share to compare groups of different sizes.
- **An interval describes the average, not the people.** A narrow confidence interval says the mean is well pinned down; it says nothing about how far individual rows spread, which the standard deviation shows.
- **Survey data may need a weight.** If the rows do not represent the population evenly, an unweighted average describes the sample, not the population; set the request's weight.
<!-- docgen:preserve end guide-describe-pitfalls -->

## Next steps

**Related questions:**

- [Distribution shape](distribution_shape.md)
- [Mix and share](composition.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`overlay-formula-namespace`](../skills/overlay-formula-namespace.md), [`overlay-pairwise`](../skills/overlay-pairwise.md), [`overlay-reference-tests`](../skills/overlay-reference-tests.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`panel-n-sources`](../skills/panel-n-sources.md), [`window-design`](../skills/window-design.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-agg-ci-lower`](../skills/op-agg-ci-lower.md), [`op-agg-ci-upper`](../skills/op-agg-ci-upper.md)

**From the command line:**

- `pulse recommend --intent describe --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
