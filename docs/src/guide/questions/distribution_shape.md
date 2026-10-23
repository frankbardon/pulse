# Distribution shape

## The question

<!-- docgen:preserve begin guide-distribution_shape-question -->
You have one numeric measure — income, order value, a duration, a test score — and you want to know its shape before you summarise or test it: is it roughly a bell curve, lopsided with a long tail, flat or peaked, and are there values far out from the rest?

It sounds like:

- "Is income skewed?"
- "Does this score follow a normal distribution, so a t-test is safe?"
- "Is the spend of the top customers far out from everyone else?"

Start with the figures that describe shape: the spread (standard deviation, range, percentiles), the skewness (lopsidedness) and the kurtosis (how heavy the tails are). Add a histogram-style grouping to see it, and a normality test only when a later step assumes a bell shape. To compare one group's shape with another's, use a two-sample Kolmogorov-Smirnov test.
<!-- docgen:preserve end guide-distribution_shape-question -->

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

**Is customer income skewed, or does it follow a bell curve?**

The request (example `shape_moments`; `pulse examples show shape_moments` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_AVERAGE",
      "field": "income",
      "label": "mean_income"
    },
    {
      "type": "AGG_STDDEV",
      "field": "income",
      "label": "stddev_income"
    },
    {
      "type": "AGG_SKEWNESS",
      "field": "income",
      "label": "skew_income"
    },
    {
      "type": "AGG_KURTOSIS",
      "field": "income",
      "label": "kurt_income"
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
      "kurt_income": 4.908,
      "mean_income": 4.804e+04,
      "skew_income": 1.825,
      "stddev_income": 3.225e+04
    }
  ],
  "metadata": {
    "total_rows": 200,
    "filtered_rows": 200,
    "cohort_file": "customers.pulse"
  },
  "returned": {
    "preset": "standard",
    "digest": "rp1:d1fda7b27cbd73c09073bf49c8d90fbde3de523876628808c3b5ee17a5825705",
    "precision": 4
  }
}
```

It is clearly skewed to the right. Skewness is well above zero, which means a long tail of high earners pulls the average up, and the excess kurtosis is well above zero too, so extreme incomes turn up far more often than a bell curve would predict. The standard deviation is about two thirds of the mean, another sign of a wide, lopsided spread. For a measure shaped like this, report the median beside the mean, and consider a log transform before a test that assumes a bell shape. On a sample of this size a single extreme income can move skewness and kurtosis a lot, so read percentiles or a histogram beside them.

## How to read the result

### `AGG_KURTOSIS`

**`value`**

How heavy the tails are next to a normal distribution. Pulse computes the population EXCESS kurtosis g2: the average fourth-power z-score minus 3, (m4/n) / (m2/n)^2 - 3, where the components m2 and m4 are SUMS of squared and fourth-power distances from the mean (not yet divided by n), so a normal distribution scores 0. It can never fall below -2.

**Sign:**

- `+`: heavier tails than a normal distribution: extreme values turn up more often than the spread suggests
- `-`: lighter tails than a normal distribution: values far from the mean turn up less often than the spread suggests, as in a flat (uniform) or two-humped spread; it does not mean the values are bounded

**Caveats:**

- There are no sourced bands for kurtosis: the cut-offs quoted for it (such as 2 or 7) are pass/fail normality screens from particular fields, disagree on whether 3 has been subtracted, and are not graded readings.
- Small-n bias: g2 differs from the adjusted G2 that Excel KURT and SPSS print, and on small groups the gap is large; neither is stable on a handful of rows.
- It reads null when no row has a value, when n is 1 or when the variance is zero (every value the same): no shape can be seen there. A 0 is a real, balanced reading; on a tiny group check components n before trusting it.
- It measures the tails, not how pointed the peak looks, and a single extreme value can dominate it.

More on reading it: [AGG_KURTOSIS](../reading/descriptive.md#op-agg_kurtosis).

### `AGG_SKEWNESS`

**`value`**

How lopsided the values are around their mean. Pulse computes the population moment coefficient g1: the average cubed z-score, (m3/n) / (m2/n)^1.5, where the components m2 and m3 are SUMS of squared and cubed distances from the mean (not yet divided by n). 0 means the values balance around the mean.

**Sign:**

- `+`: a longer or heavier tail to the right: a few values sit far above the rest
- `-`: a longer or heavier tail to the left: a few values sit far below the rest

**Caveats:**

- The sign does not fix whether the mean sits above or below the median: the two often disagree on whole-number or many-peaked fields, so read AGG_MEDIAN beside AGG_AVERAGE for that.
- There are no sourced bands for skewness: the 0.5 / 1 rules of thumb in circulation trace to secondary quotations that could not be checked against their source, so compare values with each other rather than with a fixed cut-off.
- Small-n bias: g1 runs smaller in size than the adjusted G1 that Excel SKEW, SPSS and SAS print (G1 = g1 * sqrt(n(n-1)) / (n-2)), so the two disagree on small groups and converge as n grows; on a handful of rows neither is stable.
- It reads null when no row has a value, when n is 1 or when the variance is zero (every value the same): no shape can be seen there. A 0 is a real, balanced reading; on a tiny group check components n before trusting it.
- Deviations are cubed, so one extreme value can set the sign and size on its own.

More on reading it: [AGG_SKEWNESS](../reading/descriptive.md#op-agg_skewness).

### `AGG_STDDEV`

**`value`**

The typical distance of a value from the group's mean, in the field's own units. 0 means every value is the same; for a roughly bell-shaped field about two thirds of the values sit within one standard deviation of the mean.

**Caveats:**

- It is the spread of the rows, not the precision of the mean: for how precisely the average is known, read AGG_CI_LOWER / AGG_CI_UPPER.
- Pulse divides by n (the population form); most tools report the sample form, dividing by n - 1, which is slightly larger. The gap matters only on small groups; AGG_WELFORD gives the sample form.
- Each distance is squared before averaging, so a few extreme values inflate it; for a skewed field a percentile range describes the spread better.
- A group with a single value reads 0: no spread can be seen in one value, which is not the same as a group whose values agree.

More on reading it: [AGG_STDDEV](../reading/descriptive.md#op-agg_stddev).

## Pitfalls

<!-- docgen:preserve begin guide-distribution_shape-pitfalls -->
- **Skewness and kurtosis move with a few rows.** One extreme value can change both a lot, especially on a small sample; look at the percentiles or a histogram before you rest a decision on them.
- **A normality test answers a narrow question.** On a large sample it flags departures from a bell curve too small to matter; on a small one it misses large ones. Ask whether the departure is big enough to affect the test you plan to run.
- **Most real measures are not bell-shaped.** Money, durations and counts usually have a long upper tail; that is a feature of the data, not an error. Report the median beside the mean, or work on a log scale.
- **An outlier is a lead, not a mistake.** A value far from the rest may be a recording error or your most important customer; check it before you trim or cap it.
- **Kurtosis here is excess kurtosis.** A bell curve scores zero, not three; a positive figure means heavier tails than a bell curve, a negative one lighter.
- **Shape depends on the population you filtered.** Mixing two groups with different typical values can produce two humps or a false skew; check the shape within each group as well.
<!-- docgen:preserve end guide-distribution_shape-pitfalls -->

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Segment into groups](segment.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`facet-design`](../skills/facet-design.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-formula-namespace`](../skills/overlay-formula-namespace.md), [`overlay-pairwise`](../skills/overlay-pairwise.md), [`overlay-reference-tests`](../skills/overlay-reference-tests.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`panel-n-sources`](../skills/panel-n-sources.md), [`statistical-testing`](../skills/statistical-testing.md), [`test-variant-options`](../skills/test-variant-options.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-agg-kurtosis`](../skills/op-agg-kurtosis.md), [`op-agg-skewness`](../skills/op-agg-skewness.md), [`op-agg-stddev`](../skills/op-agg-stddev.md)

**From the command line:**

- `pulse recommend --intent distribution_shape --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
