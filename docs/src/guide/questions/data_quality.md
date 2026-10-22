# Data quality

## The question

<!-- docgen:preserve begin guide-data_quality-question -->
Before you trust an analysis, you want to know whether the data behind it is sound: how much is missing, whether any rows are implausible or far out of line with the rest, and whether some respondents answered carelessly.

It sounds like:

- "How much is missing?"
- "Are there customers whose figures look implausible?"
- "Who answered carelessly?"

Count what is missing per field (a null count beside a count of present values), and keep or drop the null rows with a null filter. For rows out of line on several fields at once, score each row's distance from the cohort's centre (Mahalanobis distance) or its leverage in a model. For a rating battery, flag rows that give the same answer to every item (straight-lining) or answer too few items to score.
<!-- docgen:preserve end guide-data_quality-question -->

## Pick your analysis

This question fits data shaped like:

- `fields`: 1 or more numeric, categorical, date, bool or set fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_DISTINCT_COUNT`](../catalog/aggregator.md#op-agg_distinct_count) | Number of different values a field takes, over all rows or per group. | basic |
| [`AGG_MAX`](../catalog/aggregator.md#op-agg_max) | Largest value of a numeric or date field, over all rows or per group. | basic |
| [`AGG_MIN`](../catalog/aggregator.md#op-agg_min) | Smallest value of a numeric or date field, over all rows or per group. | basic |
| [`AGG_NULL_COUNT`](../catalog/aggregator.md#op-agg_null_count) | Number of rows where a field has no value, over all rows or per group. | basic |

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_MAHALANOBIS`](../catalog/attribute.md#op-attr_mahalanobis) | Adds to every row how unusual its mix of several numeric fields is, allowing for how those fields usually vary together. | advanced |
| [`ATTR_REG_LEVERAGE`](../catalog/attribute.md#op-attr_reg_leverage) | Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit. | advanced |
| [`ATTR_REG_RESIDUAL`](../catalog/attribute.md#op-attr_reg_residual) | Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it. | advanced |

### Filterers

| Use | In plain words | Level |
|---|---|---|
| [`FILTER_EXCLUDE`](../catalog/filterer.md#op-filter_exclude) | Drops the rows whose field holds one of the listed values and keeps the rest, such as removing test accounts. | basic |
| [`FILTER_NULL`](../catalog/filterer.md#op-filter_null) | Keeps either the rows where a field is missing or the rows where it is present, to find gaps or set them aside. | basic |
| [`FILTER_RANGE`](../catalog/filterer.md#op-filter_range) | Keeps only the rows whose number lies between a low and a high limit, both limits included. | basic |
| [`FILTER_EXPRESSION`](../catalog/filterer.md#op-filter_expression) | Keeps the rows for which a written yes-or-no condition is true, for rules the other filters cannot state, such as two fields together. | intermediate |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_BOX_M`](../catalog/test.md#op-test_box_m) | Checks whether several groups share one spread pattern across a set of numeric fields, the assumption behind comparing average profiles. | advanced |
| [`TEST_MARDIA`](../catalog/test.md#op-test_mardia) | Checks whether a set of numeric fields looks jointly normal, through their combined skew and tail weight. | advanced |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_COLLINEARITY`](../catalog/matrix.md#op-mat_collinearity) | Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled. | intermediate |

## A worked example

**Is any customer's age and income combination far out of line with the rest?**

The request (example `attr_mahalanobis`; `pulse examples show attr_mahalanobis` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_AVERAGE",
      "field": "d2",
      "label": "mean_d2"
    },
    {
      "type": "AGG_MAX",
      "field": "d2",
      "label": "max_d2"
    },
    {
      "type": "AGG_MIN",
      "field": "p_outlier",
      "label": "min_p"
    }
  ],
  "attributes": [
    {
      "type": "ATTR_MAHALANOBIS",
      "label": "d2",
      "fields": [
        "age",
        "income"
      ]
    },
    {
      "type": "ATTR_MAHALANOBIS",
      "label": "p_outlier",
      "fields": [
        "age",
        "income"
      ],
      "params": {
        "output": "p",
        "fit_on": [
          {
            "type": "FILTER_INCLUDE",
            "field": "region",
            "values": [
              "north",
              "south"
            ]
          }
        ]
      }
    }
  ],
  "cohort": {
    "filename": "customers.pulse",
    "data_dir": ".data"
  },
  "return": {
    "preset": "standard",
    "precision": 4
  }
}
```

The result:

```json
{
  "data": [
    {
      "max_d2": 30.34,
      "mean_d2": 1.99,
      "min_p": 1.869e-08
    }
  ],
  "metadata": {
    "total_rows": 200,
    "filtered_rows": 200,
    "cohort_file": "customers.pulse"
  },
  "returned": {
    "preset": "standard",
    "digest": "rp1:e54405ae11f7db2bbe9d6bcfa4b9a169857bd482f45431aaae78605ca90255ef",
    "precision": 4
  }
}
```

Yes, at least one. Mahalanobis distance measures how far a row sits from the centre of the cohort once the spread and correlation of age and income are taken into account; across the rows it averages about the number of fields, which is what a well-behaved cloud gives. The largest distance is many times that average and the smallest outlier p-value is tiny, so at least one customer sits well outside the usual profile. The p-value is fitted on the north and south regions only, so east and west customers are judged against that profile. An unusual row is a lead to check, not an error to delete: income here has a long upper tail, so the most distant customers are most likely genuine high earners, and the chi-square p-value assumes a bell-shaped cloud that a skewed income does not have.

## How to read the result

### `ATTR_MAHALANOBIS`

**`value`**

With output d2 (the default): the row's squared Mahalanobis distance D² = (x - mean)' S^-1 (x - mean) across the p fields, the distance from the fitted average row in units of the fields' own spread and correlation; 0 is exactly average and it is never negative. With output p: the chance of a D² at least this large under a chi-square curve on p degrees of freedom, so a small value marks an unusual row.

**Caveats:**

- D² grows with the number of fields: over the fitted rows it averages about p, so compare it with p (or read output p), never with a fixed cut-off across batteries.
- The p output assumes the fields are jointly normal; on skewed or bounded fields (rating scales) it is only a ranking, and screening many rows flags some by chance.
- Extreme rows in the fit pull the mean and inflate the spread, masking themselves and each other; fit_on can fit on a clean subset while still scoring every row.
- The fit is over every filtered row (or the fit_on subset), not the row's group. Under a row weight the mean and covariance are weighted; a row with no usable weight is left out of the fit but still scored.
- A row missing any field reads null, and every row reads null, with a warning, when the fit has no more complete rows than fields or a field is constant or a combination of others.

More on reading it: [ATTR_MAHALANOBIS](../reading/descriptive.md#op-attr_mahalanobis).

## Pitfalls

<!-- docgen:preserve begin guide-data_quality-pitfalls -->
- **Missing is not zero.** Aggregates leave null rows out and report how many they skipped (`n_null`); a filter or formula that turns a null into zero changes every figure after it.
- **Check what the missing rows have in common.** If the rows with missing answers come mostly from one group, dropping them shifts the comparison; compare the groups' missing rates first.
- **An unusual row is a lead, not an error.** A customer far from the centre may be a typo or a genuine high earner; check it before you delete or cap it.
- **Distance-based outlier scores assume a bell-shaped cloud.** On a skewed measure such as income the tail rows get extreme scores and tiny p-values even when they are genuine; judge them against the measure's shape.
- **Many rows tested means some flagged by luck.** Scoring every row against a cut-off flags a few ordinary rows on any data set; look at how far beyond the cut-off a row falls.
- **Fix the data, then rerun.** A quality check is worth doing before the analysis it protects; rerun the analysis after you exclude or correct rows and say what changed.
<!-- docgen:preserve end guide-data_quality-pitfalls -->

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Compare groups](compare_groups.md)
- [Find drivers](drivers.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`statistical-testing`](../skills/statistical-testing.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-agg-max`](../skills/op-agg-max.md), [`op-agg-min`](../skills/op-agg-min.md), [`op-attr-mahalanobis`](../skills/op-attr-mahalanobis.md), [`op-filter-include`](../skills/op-filter-include.md)

**From the command line:**

- `pulse recommend --intent data_quality --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
