# Data quality

## The question

Questions of this kind sound like:

- Is this data trustworthy?
- Who answered carelessly?
- How much is missing?

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

No worked example is published for this question yet. `pulse recommend --intent data_quality` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Compare groups](compare_groups.md)
- [Find drivers](drivers.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`statistical-testing`](../skills/statistical-testing.md)

**From the command line:**

- `pulse recommend --intent data_quality --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
