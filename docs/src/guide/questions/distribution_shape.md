# Distribution shape

## The question

Questions of this kind sound like:

- Is it normally distributed?
- Are there outliers?
- Is it skewed?

## Pick your analysis

This question fits data shaped like:

- `measure`: 1 or more numeric fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_MEDIAN`](../catalog/aggregator.md#op-agg_median) | Middle value of a numeric field once sorted: the typical row, not pulled by a few extreme values. | basic |
| [`AGG_PERCENTILE`](../catalog/aggregator.md#op-agg_percentile) | Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time. | basic |
| [`AGG_RANGE`](../catalog/aggregator.md#op-agg_range) | Distance between the smallest and largest value of a numeric field, over all rows or per group. | basic |
| [`AGG_STDDEV`](../catalog/aggregator.md#op-agg_stddev) | Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group. | basic |
| [`AGG_SKEWNESS`](../catalog/aggregator.md#op-agg_skewness) | How lopsided a numeric field is: positive when a long tail runs to high values, negative when it runs to low ones. | intermediate |
| [`AGG_VARIANCE`](../catalog/aggregator.md#op-agg_variance) | Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group. | intermediate |
| [`AGG_VEC_SD`](../catalog/aggregator.md#op-agg_vec_sd) | Standard deviation of each item of a battery or mix, as one list per group: how much the group's rows disagree on each item. | intermediate |
| [`AGG_KURTOSIS`](../catalog/aggregator.md#op-agg_kurtosis) | How heavy a numeric field's tails are next to a normal distribution: positive when extreme values are more common. | advanced |

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_PERCENTILE`](../catalog/attribute.md#op-attr_percentile) | Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied. | basic |

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_RANGE`](../catalog/grouper.md#op-group_range) | Splits a number into bands of equal width, such as ages 20-30 and 30-40, to see how values spread or to compare bands. | basic |
| [`GROUP_ROUNDED`](../catalog/grouper.md#op-group_rounded) | Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple. | basic |
| [`GROUP_QUANTILE`](../catalog/grouper.md#op-group_quantile) | Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom. | intermediate |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_LOG`](../catalog/feature.md#op-feat_log) | Adds a column holding the natural log of 1 + the value, which pulls in a long tail of large values so a skewed field is easier to model. | intermediate |
| [`FEAT_SQRT`](../catalog/feature.md#op-feat_sqrt) | Adds a column holding the square root of the value, a gentler squeeze than a log for counts with a moderate long tail. | intermediate |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_KS`](../catalog/test.md#op-test_ks) | Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape. | intermediate |
| [`TEST_SHAPIRO_WILK`](../catalog/test.md#op-test_shapiro_wilk) | Checks whether a numeric field looks normally distributed, overall or within each group. | intermediate |
| [`TEST_BROWN_FORSYTHE`](../catalog/test.md#op-test_brown_forsythe) | Checks whether the spread of a numeric field differs across groups: a robust test of equal variances. | advanced |
| [`TEST_MARDIA`](../catalog/test.md#op-test_mardia) | Checks whether a set of numeric fields looks jointly normal, through their combined skew and tail weight. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_KS_VS_POP`](../catalog/overlay.md#op-overlay_ks_vs_pop) | Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent distribution_shape` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Segment into groups](segment.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`facet-design`](../skills/facet-design.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`statistical-testing`](../skills/statistical-testing.md)

**From the command line:**

- `pulse recommend --intent distribution_shape --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
