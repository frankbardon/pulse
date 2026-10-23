# Data quality

## The question

<!-- docgen:preserve begin guide-data_quality-question -->
Before you trust an analysis, you want to know whether the data behind it is sound: how much is missing, whether the gaps fall evenly or pile up in one group, whether any rows are implausible or far out of line with the rest, and whether some respondents answered carelessly.

It sounds like:

- "How much is missing?"
- "Is the missing data concentrated in one channel or group?"
- "Are there customers whose figures look implausible?"
- "Who answered carelessly?"

Count what is missing per field (a null count beside a count of present values), then group the same counts by a field that could explain the gaps, such as the channel a form arrived through. Keep or drop the null rows with a null filter once you know who they are. For rows out of line on several fields at once, score each row's distance from the cohort's centre (Mahalanobis distance) or its leverage in a model (how strongly that one row pulls the fitted line towards itself). For a rating battery, flag rows that give the same answer to every item (straight-lining) or answer too few items to score.
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

**How much income is missing, and is it missing evenly across intake channels?**

The request (example `agg_missing_by_channel`; `pulse examples show agg_missing_by_channel` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_COUNT",
      "field": "income",
      "label": "income_present"
    },
    {
      "type": "AGG_NULL_COUNT",
      "field": "income",
      "label": "income_null"
    },
    {
      "type": "AGG_NULL_COUNT",
      "field": "age",
      "label": "age_null"
    }
  ],
  "cohort": {
    "filename": "intake.pulse",
    "data_dir": ".data"
  },
  "groups": [
    {
      "type": "GROUP_CATEGORY",
      "field": "channel"
    }
  ],
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
      "age_null": 3,
      "channel": "paper",
      "income_null": 52,
      "income_present": 48
    },
    {
      "age_null": 7,
      "channel": "phone",
      "income_null": 16,
      "income_present": 84
    },
    {
      "age_null": 4,
      "channel": "web",
      "income_null": 7,
      "income_present": 93
    }
  ],
  "metadata": {
    "total_rows": 300,
    "filtered_rows": 300,
    "cohort_file": "intake.pulse"
  },
  "returned": {
    "preset": "standard",
    "digest": "rp1:d1fda7b27cbd73c09073bf49c8d90fbde3de523876628808c3b5ee17a5825705",
    "precision": 4
  }
}
```

Not evenly. Every channel has some blank incomes, but about half of the paper forms leave income blank, far more than the web forms, with phone in between, while age is blank on only a handful of forms in every channel. Missing income therefore depends on how the form arrived, so an average income over the present rows leans towards web applicants; compare the channels, or weight them back, before you report one figure for everyone. A null here is a blank answer, not a zero income: the counts of present values leave the blanks out instead of treating them as nothing earned.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

<!-- docgen:preserve begin guide-data_quality-pitfalls -->
- **Missing is not zero.** Aggregates leave null rows out and report how many they skipped (`n_null`); a filter or formula that turns a null into zero changes every figure after it.
- **Check what the missing rows have in common.** If the blanks come mostly from one group, a figure over the present rows leans towards the groups that answered; compare the groups' missing rates before you drop or ignore the blanks.
- **Dropping null rows is a choice, not a cleanup.** A null filter shrinks every group by a different amount when the gaps are uneven; say how many rows each group lost.
- **A small, even gap is usually harmless.** A field blank on a few rows in every group rarely moves a comparison; an uneven gap on the field you are reporting does.
- **An unusual row is a lead, not an error.** A customer far from the centre may be a typo or a genuine high earner; check it before you delete or cap it.
- **Fix the data, then rerun.** A quality check is worth doing before the analysis it protects; rerun the analysis after you exclude or correct rows and say what changed.
<!-- docgen:preserve end guide-data_quality-pitfalls -->

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Compare groups](compare_groups.md)
- [Find drivers](drivers.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`statistical-testing`](../skills/statistical-testing.md), [`test-variant-options`](../skills/test-variant-options.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-agg-null-count`](../skills/op-agg-null-count.md), [`op-group-category`](../skills/op-group-category.md)

**From the command line:**

- `pulse recommend --intent data_quality --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
