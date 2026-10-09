# Aggregators

The aggregators this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`AGG_AVERAGE`](#op-agg_average) | Average of a numeric field, over all rows or per group. | What is the average order value? | basic | [`AGG_MEDIAN`](#op-agg_median) when the field is skewed or has extreme values and you want the typical row.<br>[`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) when rows carry weights.<br>[`AGG_WELFORD`](#op-agg_welford) when you also need the spread around the average. |
| [`AGG_CI_LOWER`](#op-agg_ci_lower) | Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. | How precisely do we know the average satisfaction in each segment? | intermediate | [`AGG_CI_UPPER`](#op-agg_ci_upper) when you want the upper end of the same interval.<br>[`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two group means differ.<br>[`AGG_PERCENTILE`](#op-agg_percentile) when you want the range individual rows fall in, not the mean. |
| [`AGG_CI_UPPER`](#op-agg_ci_upper) | Upper end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. | What is the highest average wait time the data are consistent with at 95% confidence? | intermediate | [`AGG_CI_LOWER`](#op-agg_ci_lower) when you want the lower end of the same interval.<br>[`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two group means differ. |
| [`AGG_COUNT`](#op-agg_count) | Number of rows that have a value in a field, over all rows or per group. | How many responses did each region return? | basic | [`AGG_NULL_COUNT`](#op-agg_null_count) when you want the rows where the field is empty.<br>[`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) when you want how many different values appear, not how many rows.<br>[`GROUP_CATEGORY`](grouper.md#op-group_category) when you want a count for every value of the field: group by it, then count.<br>[`AGG_FREQUENCY`](#op-agg_frequency) when you want how many rows hold one particular value, such as the Yes answers. |
| [`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) | Number of different values a field takes, over all rows or per group. | How many different customers ordered this month? | basic | [`AGG_COUNT`](#op-agg_count) when you want how many rows there are, not how many different values.<br>[`GROUP_CATEGORY`](grouper.md#op-group_category) when you want how many rows hold each value: group by the field, then count.<br>[`AGG_SET_DISTINCT_VALUES`](#op-agg_set_distinct_values) when the field is a multi-select and each combination is the unit. |
| [`AGG_DISTINCT_SUM`](#op-agg_distinct_sum) | Total of a numeric field counting each key once, such as a respondent's weight summed once per respondent. | What is the weighted base when each respondent appears on several rows? | intermediate | [`AGG_SUM`](#op-agg_sum) when every row is its own unit and should be added.<br>[`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) when you want how many keys there are, not a total. |
| [`AGG_FREQUENCY`](#op-agg_frequency) | How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group. | How many respondents answered Yes in each region? | basic | [`AGG_MODE_COUNT`](#op-agg_mode_count) when you want how many rows share the field's most common value, whatever it is.<br>[`GROUP_CATEGORY`](grouper.md#op-group_category) when you want a count for every value of the field: group by it, then count rows.<br>[`FILTER_INCLUDE`](filterer.md#op-filter_include) when you want rows holding any of several values: keep them, then count rows.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when the field is a multi-select. |
| [`AGG_KURTOSIS`](#op-agg_kurtosis) | How heavy a numeric field's tails are next to a normal distribution: positive when extreme values are more common. | Do transaction amounts have more extreme values than a bell curve would give? | advanced | [`AGG_SKEWNESS`](#op-agg_skewness) when you want to know whether the field is lopsided.<br>[`TEST_SHAPIRO_WILK`](test.md#op-test_shapiro_wilk) when you want a test of whether the field follows a normal shape. |
| [`AGG_MAX`](#op-agg_max) | Largest value of a numeric or date field, over all rows or per group. | What is the latest order date in each region? | basic | [`AGG_MIN`](#op-agg_min) when you want the smallest value.<br>[`AGG_PERCENTILE`](#op-agg_percentile) when one extreme row would mislead and you want a high value that ignores it.<br>[`AGG_RANGE`](#op-agg_range) when you want the distance between smallest and largest. |
| [`AGG_MEDIAN`](#op-agg_median) | Middle value of a numeric field once sorted: the typical row, not pulled by a few extreme values. | What is the typical household income in each region? | basic | [`AGG_PERCENTILE`](#op-agg_percentile) when you want a value other than the middle, such as the 90th percentile.<br>[`AGG_AVERAGE`](#op-agg_average) when the field is roughly symmetric and you want a figure that combines across groups.<br>[`AGG_MODE`](#op-agg_mode) when the field holds categories rather than numbers. |
| [`AGG_MIN`](#op-agg_min) | Smallest value of a numeric or date field, over all rows or per group. | What is the earliest order date in each region? | basic | [`AGG_MAX`](#op-agg_max) when you want the largest value.<br>[`AGG_PERCENTILE`](#op-agg_percentile) when one extreme row would mislead and you want a low value that ignores it.<br>[`AGG_RANGE`](#op-agg_range) when you want the distance between smallest and largest. |
| [`AGG_MODE`](#op-agg_mode) | Most common value of a field, over all rows or per group. | What is the most common answer to the preferred-brand question? | basic | [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want the count of every value, not only the top one: group by the field, then count.<br>[`AGG_MEDIAN`](#op-agg_median) when the field is numeric and you want its middle value.<br>[`AGG_FREQUENCY`](#op-agg_frequency) when you want how many rows hold one particular value, such as the Yes answers.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when the field is a multi-select. |
| [`AGG_MODE_COUNT`](#op-agg_mode_count) | How many rows share a field's most common value, over all rows or per group. | How many respondents gave the most common answer? | basic | [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want a count for every value of the field: group by it, then count rows.<br>`capability:facet` when you want per-value counts for several fields in one call.<br>[`AGG_MODE`](#op-agg_mode) when you want which value is most common, not how many rows hold it.<br>[`AGG_FREQUENCY`](#op-agg_frequency) when you want how many rows hold one particular value, such as the Yes answers.<br>[`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) when you want how many different values there are.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when the field is a multi-select. |
| [`AGG_NULL_COUNT`](#op-agg_null_count) | Number of rows where a field has no value, over all rows or per group. | How many respondents skipped the income question? | basic | [`AGG_COUNT`](#op-agg_count) when you want the rows that do have a value.<br>[`FILTER_NULL`](filterer.md#op-filter_null) when you want to keep or drop the rows with no value. |
| [`AGG_PERCENTILE`](#op-agg_percentile) | Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time. | How long do the slowest 10% of deliveries take in each region? | basic | [`AGG_MEDIAN`](#op-agg_median) when you want the 50th percentile.<br>[`ATTR_PERCENTILE`](attribute.md#op-attr_percentile) when you want each row's own percentile position.<br>[`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want rows split into equal-sized bands. |
| [`AGG_RANGE`](#op-agg_range) | Distance between the smallest and largest value of a numeric field, over all rows or per group. | How far apart are the cheapest and most expensive orders? | basic | [`AGG_STDDEV`](#op-agg_stddev) when you want a spread measure that uses every row, not only the two extremes.<br>[`AGG_PERCENTILE`](#op-agg_percentile) when you want the spread of the middle of the data, ignoring extremes. |
| [`AGG_RATIO`](#op-agg_ratio) | Total of one field divided by the total of another, over all rows or per group, such as revenue per order. | What is revenue per visit in each channel? | basic | [`ATTR_FORMULA`](attribute.md#op-attr_formula) when you want the average of each row's own ratio: compute it per row (guarding a 0 denominator), then average it.<br>[`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) when you want an average where some rows count more than others. |
| [`AGG_SET_CARDINALITY_AVG`](#op-agg_set_cardinality_avg) | For a multi-select field, the average number of options chosen per row. | How many brands does a respondent recognise on average? | basic | [`AGG_SET_CARDINALITY_SUM`](#op-agg_set_cardinality_sum) when you want the total number of selections.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want which options were chosen. |
| [`AGG_SET_CARDINALITY_SUM`](#op-agg_set_cardinality_sum) | For a multi-select field, the total number of options chosen across all rows. | How many brand mentions did the survey collect in total? | basic | [`AGG_SET_CARDINALITY_AVG`](#op-agg_set_cardinality_avg) when you want the number per row.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want the count for each option. |
| [`AGG_SET_DISTINCT_VALUES`](#op-agg_set_distinct_values) | For a multi-select field, how many different combinations of options appear. | How many different combinations of channels do customers use? | basic | [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want the count of each single option.<br>[`AGG_SET_UNION`](#op-agg_set_union) when you want every option chosen by anyone.<br>[`GROUP_SET_VALUE`](grouper.md#op-group_set_value) when you want how common each combination is: a count alone cannot show whether one dominates. |
| [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) | For a multi-select field, how many rows chose each option, over all rows or per group. | How many respondents selected each brand they have heard of? | basic | [`GROUP_CATEGORY`](grouper.md#op-group_category) when the field holds one value per row: group by it, then count.<br>[`AGG_SET_DISTINCT_VALUES`](#op-agg_set_distinct_values) when you want how many different combinations were chosen. |
| [`AGG_SET_INTERSECTION`](#op-agg_set_intersection) | For a multi-select field, the options chosen by every row, over all rows or per group. | Which brands did every respondent in this segment recognise? | basic | [`AGG_SET_UNION`](#op-agg_set_union) when you want every option chosen by anyone.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want how many rows chose each option. |
| [`AGG_SET_UNION`](#op-agg_set_union) | For a multi-select field, every option chosen by at least one row, over all rows or per group. | Which brands were mentioned by anyone in this segment? | basic | [`AGG_SET_INTERSECTION`](#op-agg_set_intersection) when you want the options every row chose.<br>[`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want how many rows chose each option. |
| [`AGG_SKEWNESS`](#op-agg_skewness) | How lopsided a numeric field is: positive when a long tail runs to high values, negative when it runs to low ones. | Are incomes in each region bunched low with a few very high ones? | intermediate | [`AGG_KURTOSIS`](#op-agg_kurtosis) when you want to know whether the tails are heavy rather than lopsided.<br>[`TEST_SHAPIRO_WILK`](test.md#op-test_shapiro_wilk) when you want a test of whether the field follows a normal shape.<br>[`AGG_MEDIAN`](#op-agg_median) when you want the typical value of a skewed field. |
| [`AGG_STDDEV`](#op-agg_stddev) | Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group. | How much do delivery times vary around the average in each region? | basic | [`AGG_CI_LOWER`](#op-agg_ci_lower) when you want how precisely the average itself is known.<br>[`AGG_WELFORD`](#op-agg_welford) when you want the sample form (dividing by n - 1) together with the mean and n.<br>[`AGG_PERCENTILE`](#op-agg_percentile) when the field has extreme values and you want a spread they cannot drag. |
| [`AGG_SUM`](#op-agg_sum) | Total of a numeric field, over all rows or per group. | What is total revenue by region? | basic | [`AGG_AVERAGE`](#op-agg_average) when you want the typical value per row rather than the total.<br>[`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) when rows carry weights and you want a weighted average.<br>[`AGG_DISTINCT_SUM`](#op-agg_distinct_sum) when a value repeats on several rows of the same key and must be added once per key.<br>[`WIN_RUNNING_SUM`](window.md#op-win_running_sum) when you want the total so far, row by row. |
| [`AGG_VARIANCE`](#op-agg_variance) | Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group. | How much does the order value vary within each channel, in squared units? | intermediate | [`AGG_STDDEV`](#op-agg_stddev) when you want the spread in the field's own units, which is easier to read.<br>[`AGG_WELFORD`](#op-agg_welford) when you want the sample variance (dividing by n - 1) with the mean and n, as pooling and study-size planning expect. |
| [`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) | Average of a numeric field where each row counts in proportion to a weight field, such as a survey weight. | What is the weighted average satisfaction per region? | intermediate | [`AGG_AVERAGE`](#op-agg_average) when every row should count equally.<br>[`AGG_RATIO`](#op-agg_ratio) when you want one total divided by another. |
| [`AGG_WELFORD`](#op-agg_welford) | Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs. | What are the mean, variance and size of each cell, so cells can be tested against each other? | intermediate | [`AGG_AVERAGE`](#op-agg_average) when you want only the average.<br>[`AGG_STDDEV`](#op-agg_stddev) when you want the population spread (dividing by n) on its own.<br>[`TEST_WELCH`](test.md#op-test_welch) when you want the comparison itself on raw rows. |
| [`AGG_ZSCORE`](#op-agg_zscore) | Population mean and standard deviation of a numeric field, for standardizing it; the value itself is always 0. | What centre and scale standardize this field in each group? | intermediate | [`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want a z-score on every row.<br>[`OVERLAY_ZSCORE_VS_TOTAL`](overlay.md#op-overlay_zscore_vs_total) when you want each group's value against all groups.<br>[`AGG_STDDEV`](#op-agg_stddev) when you want only the spread. |

## Operators

<a id="op-agg_average"></a>

### `AGG_AVERAGE`

Average of a numeric field, over all rows or per group.

**Level:** basic

**Also known as:** `mean`, `arithmetic mean`

**Questions it answers:**

- What is the average order value?
- What is the typical rating in each region?

**Use cases by domain:**

- *survey:* Mean satisfaction score per segment.
- *ops:* Average order value by sales channel.
- *science:* Mean measurement per treatment arm.

**Assumptions:**

- Missing values are skipped: the average is over rows that have a value. A group where no row has a value reads 0, not empty, so check n in the components before reporting it.

**Use something else:**

- [`AGG_MEDIAN`](#op-agg_median) when the field is skewed or has extreme values and you want the typical row.
- [`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) when rows carry weights.
- [`AGG_WELFORD`](#op-agg_welford) when you also need the spread around the average.

**Glossary:** [`mean`](../glossary.md#term-mean), [`median`](../glossary.md#term-median), [`outlier`](../glossary.md#term-outlier), [`skew`](../glossary.md#term-skew)

**Skill:** [`op-agg-average`](../skills/op-agg-average.md)

<a id="op-agg_ci_lower"></a>

### `AGG_CI_LOWER`

Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down.

**Level:** intermediate

**Questions it answers:**

- How precisely do we know the average satisfaction in each segment?
- What is the lowest average order value the data are consistent with at 95% confidence?

**Use cases by domain:**

- *survey:* Error bars on a mean score per segment.
- *ops:* Range for average handling time per team.
- *science:* Interval for a mean measurement per arm.

**Assumptions:**

- Rows are independent draws, unweighted.
- Normal critical value (1.96 at 95%), not Student's t, so small groups, and strongly skewed fields even well past 30 rows, get an interval that is too narrow.
- Empty (NaN) when fewer than two rows have a value.

**Use something else:**

- [`AGG_CI_UPPER`](#op-agg_ci_upper) when you want the upper end of the same interval.
- [`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two group means differ.
- [`AGG_PERCENTILE`](#op-agg_percentile) when you want the range individual rows fall in, not the mean.

**Glossary:** [`confidence-interval`](../glossary.md#term-confidence-interval), [`mean`](../glossary.md#term-mean), [`standard-error`](../glossary.md#term-standard-error), [`sample-size`](../glossary.md#term-sample-size), [`independence`](../glossary.md#term-independence)

**Skill:** [`op-agg-ci-lower`](../skills/op-agg-ci-lower.md)

<a id="op-agg_ci_upper"></a>

### `AGG_CI_UPPER`

Upper end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down.

**Level:** intermediate

**Questions it answers:**

- What is the highest average wait time the data are consistent with at 95% confidence?
- How wide is the uncertainty around each region's mean score?

**Use cases by domain:**

- *survey:* Error bars on a mean score per segment.
- *ops:* Upper end of the 95% interval for average delivery time per carrier: the true average can still lie above it.
- *science:* Interval for a mean measurement per arm.

**Assumptions:**

- Rows are independent draws, unweighted.
- Normal critical value (1.96 at 95%), not Student's t, so small groups, and strongly skewed fields even well past 30 rows, get an interval that is too narrow.
- Empty (NaN) when fewer than two rows have a value.

**Use something else:**

- [`AGG_CI_LOWER`](#op-agg_ci_lower) when you want the lower end of the same interval.
- [`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two group means differ.

**Glossary:** [`confidence-interval`](../glossary.md#term-confidence-interval), [`mean`](../glossary.md#term-mean), [`standard-error`](../glossary.md#term-standard-error), [`sample-size`](../glossary.md#term-sample-size), [`independence`](../glossary.md#term-independence)

**Skill:** [`op-agg-ci-upper`](../skills/op-agg-ci-upper.md)

<a id="op-agg_count"></a>

### `AGG_COUNT`

Number of rows that have a value in a field, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How many responses did each region return?
- How many orders were placed through each channel?

**Use cases by domain:**

- *survey:* Number of answered questionnaires per segment.
- *ops:* Order count per store per day.
- *science:* Number of recorded measurements per treatment arm.

**Assumptions:**

- Rows with a missing value in the field are not counted.

**Use something else:**

- [`AGG_NULL_COUNT`](#op-agg_null_count) when you want the rows where the field is empty.
- [`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) when you want how many different values appear, not how many rows.
- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want a count for every value of the field: group by it, then count.
- [`AGG_FREQUENCY`](#op-agg_frequency) when you want how many rows hold one particular value, such as the Yes answers.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value), [`sample-size`](../glossary.md#term-sample-size)

**Skill:** [`op-agg-count`](../skills/op-agg-count.md)

<a id="op-agg_distinct_count"></a>

### `AGG_DISTINCT_COUNT`

Number of different values a field takes, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How many different customers ordered this month?
- Does the respondent ID appear once per row, or are there duplicates?

**Use cases by domain:**

- *survey:* Number of distinct respondents per wave.
- *ops:* Unique customers per store.
- *science:* Number of distinct subjects per site.

**Assumptions:**

- Missing values are not counted as a value.

**Use something else:**

- [`AGG_COUNT`](#op-agg_count) when you want how many rows there are, not how many different values.
- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want how many rows hold each value: group by the field, then count.
- [`AGG_SET_DISTINCT_VALUES`](#op-agg_set_distinct_values) when the field is a multi-select and each combination is the unit.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-agg-distinct-count`](../skills/op-agg-distinct-count.md)

<a id="op-agg_distinct_sum"></a>

### `AGG_DISTINCT_SUM`

Total of a numeric field counting each key once, such as a respondent's weight summed once per respondent.

**Level:** intermediate

**Questions it answers:**

- What is the weighted base when each respondent appears on several rows?
- What is the total contract value when each contract repeats on every line item?

**Use cases by domain:**

- *survey:* Weighted base of a segment in a cohort with one row per answer.
- *ops:* Total customer credit limit when each customer has many orders.

**Assumptions:**

- The value is the same on every row of a key: when it differs, the first value seen is kept.
- A row missing either the value or the key adds nothing.

**Use something else:**

- [`AGG_SUM`](#op-agg_sum) when every row is its own unit and should be added.
- [`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) when you want how many keys there are, not a total.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value), [`weighting`](../glossary.md#term-weighting)

**Skill:** [`op-agg-distinct-sum`](../skills/op-agg-distinct-sum.md)

<a id="op-agg_frequency"></a>

### `AGG_FREQUENCY`

How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How many respondents answered Yes in each region?
- How many orders were returned each day?

**Use cases by domain:**

- *survey:* Number of Yes answers to a question per segment.
- *ops:* Orders with the Returned status per store per day.
- *science:* Positive results per treatment arm.

**Assumptions:**

- Missing values are skipped: they are neither counted nor part of the base the share in the components divides by.
- The value is matched as a row filter matches it: a category label, otherwise a number; a value no row holds counts 0, so check its spelling.
- The result equals keeping only that value with a row filter, then counting rows.

**Use something else:**

- [`AGG_MODE_COUNT`](#op-agg_mode_count) when you want how many rows share the field's most common value, whatever it is.
- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want a count for every value of the field: group by it, then count rows.
- [`FILTER_INCLUDE`](filterer.md#op-filter_include) when you want rows holding any of several values: keep them, then count rows.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when the field is a multi-select.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-agg-frequency`](../skills/op-agg-frequency.md)

<a id="op-agg_kurtosis"></a>

### `AGG_KURTOSIS`

How heavy a numeric field's tails are next to a normal distribution: positive when extreme values are more common.

**Level:** advanced

**Questions it answers:**

- Do transaction amounts have more extreme values than a bell curve would give?
- Are response times prone to rare, very long waits?

**Use cases by domain:**

- *ops:* Spotting fields where rare extreme values dominate risk.
- *science:* Checking a measurement's tails before trusting a mean-based test.

**Assumptions:**

- Missing values are skipped.
- Population excess kurtosis g2 (dividing by n, minus 3): a normal shape scores 0, and at small n it differs from the adjusted G2 most packages print.
- It is 0 when n is 0 or 1 or every value is the same.

**Use something else:**

- [`AGG_SKEWNESS`](#op-agg_skewness) when you want to know whether the field is lopsided.
- [`TEST_SHAPIRO_WILK`](test.md#op-test_shapiro_wilk) when you want a test of whether the field follows a normal shape.

**Glossary:** [`kurtosis`](../glossary.md#term-kurtosis), [`normal-distribution`](../glossary.md#term-normal-distribution), [`outlier`](../glossary.md#term-outlier)

**Skill:** [`op-agg-kurtosis`](../skills/op-agg-kurtosis.md)

<a id="op-agg_max"></a>

### `AGG_MAX`

Largest value of a numeric or date field, over all rows or per group.

**Level:** basic

**Questions it answers:**

- What is the latest order date in each region?
- Is any order value implausibly large?

**Use cases by domain:**

- *survey:* Highest household size reported, to catch typing errors.
- *ops:* Largest single order per sales channel.
- *science:* Peak reading per run.

**Assumptions:**

- Missing values are skipped; a group where no row has a value reads 0, not empty, so check n in the components before reporting it.
- It rests on one row, so a single bad record sets it.

**Use something else:**

- [`AGG_MIN`](#op-agg_min) when you want the smallest value.
- [`AGG_PERCENTILE`](#op-agg_percentile) when one extreme row would mislead and you want a high value that ignores it.
- [`AGG_RANGE`](#op-agg_range) when you want the distance between smallest and largest.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`percentile`](../glossary.md#term-percentile)

**Skill:** [`op-agg-max`](../skills/op-agg-max.md)

<a id="op-agg_median"></a>

### `AGG_MEDIAN`

Middle value of a numeric field once sorted: the typical row, not pulled by a few extreme values.

**Level:** basic

**Questions it answers:**

- What is the typical household income in each region?
- What is the typical delivery time when a few deliveries take weeks?

**Use cases by domain:**

- *survey:* Typical income per segment when a few incomes are very large.
- *ops:* Typical order value when a few bulk orders inflate the average.
- *science:* Typical response time per condition.

**Assumptions:**

- Missing values are skipped; a group where no row has a value reads 0, not empty, so check n in the components before reporting it.
- With an even number of values it is the halfway point between the two middle values.

**Use something else:**

- [`AGG_PERCENTILE`](#op-agg_percentile) when you want a value other than the middle, such as the 90th percentile.
- [`AGG_AVERAGE`](#op-agg_average) when the field is roughly symmetric and you want a figure that combines across groups.
- [`AGG_MODE`](#op-agg_mode) when the field holds categories rather than numbers.

**Glossary:** [`median`](../glossary.md#term-median), [`mean`](../glossary.md#term-mean), [`outlier`](../glossary.md#term-outlier), [`percentile`](../glossary.md#term-percentile), [`skew`](../glossary.md#term-skew)

**Skill:** [`op-agg-median`](../skills/op-agg-median.md)

<a id="op-agg_min"></a>

### `AGG_MIN`

Smallest value of a numeric or date field, over all rows or per group.

**Level:** basic

**Questions it answers:**

- What is the earliest order date in each region?
- Are there negative or impossible values in the age field?

**Use cases by domain:**

- *survey:* Lowest age reported, to catch out-of-range answers.
- *ops:* First order date per customer segment.
- *science:* Lowest reading per sensor, to spot faulty sensors.

**Assumptions:**

- Missing values are skipped; a group where no row has a value reads 0, not empty, so check n in the components before reporting it.
- It rests on one row, so a single bad record sets it.

**Use something else:**

- [`AGG_MAX`](#op-agg_max) when you want the largest value.
- [`AGG_PERCENTILE`](#op-agg_percentile) when one extreme row would mislead and you want a low value that ignores it.
- [`AGG_RANGE`](#op-agg_range) when you want the distance between smallest and largest.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`percentile`](../glossary.md#term-percentile)

**Skill:** [`op-agg-min`](../skills/op-agg-min.md)

<a id="op-agg_mode"></a>

### `AGG_MODE`

Most common value of a field, over all rows or per group.

**Level:** basic

**Questions it answers:**

- What is the most common answer to the preferred-brand question?
- Which product is ordered most often in each store?

**Use cases by domain:**

- *survey:* Most chosen option per question and segment.
- *ops:* Most frequent payment method per channel.
- *science:* Most common category of outcome per group.

**Assumptions:**

- Missing values are skipped; a group where no row has a value reads 0, not empty, so check n in the components before reporting it.
- When several values share the top count, the smallest wins (for categories, the first in the dictionary); tie_count in the components shows it happened.

**Use something else:**

- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want the count of every value, not only the top one: group by the field, then count.
- [`AGG_MEDIAN`](#op-agg_median) when the field is numeric and you want its middle value.
- [`AGG_FREQUENCY`](#op-agg_frequency) when you want how many rows hold one particular value, such as the Yes answers.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when the field is a multi-select.

**Glossary:** [`median`](../glossary.md#term-median)

**Skill:** [`op-agg-mode`](../skills/op-agg-mode.md)

<a id="op-agg_mode_count"></a>

### `AGG_MODE_COUNT`

How many rows share a field's most common value, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How many respondents gave the most common answer?
- How many orders used the most popular payment method?
- Grouped by the same field, how many rows fall in each category?

**Use cases by domain:**

- *survey:* Size of the largest answer group per question and segment.
- *ops:* Orders carrying the most common status per day.
- *science:* Size of the most common outcome category per treatment arm.

**Assumptions:**

- Missing values are skipped, so only rows that have a value are counted.
- Grouped by the same field, each group holds one value, so the result is that group's row count.
- When several values tie for most common the count is the same; the components name the smallest tied value as mode_value.

**Use something else:**

- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want a count for every value of the field: group by it, then count rows.
- `capability:facet` when you want per-value counts for several fields in one call.
- [`AGG_MODE`](#op-agg_mode) when you want which value is most common, not how many rows hold it.
- [`AGG_FREQUENCY`](#op-agg_frequency) when you want how many rows hold one particular value, such as the Yes answers.
- [`AGG_DISTINCT_COUNT`](#op-agg_distinct_count) when you want how many different values there are.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when the field is a multi-select.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-agg-mode-count`](../skills/op-agg-mode-count.md)

<a id="op-agg_null_count"></a>

### `AGG_NULL_COUNT`

Number of rows where a field has no value, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How many respondents skipped the income question?
- Which regions have the most orders missing a delivery date?

**Use cases by domain:**

- *survey:* Item non-response per question and per segment.
- *ops:* Records missing a required field, per data source.
- *science:* Missing measurements per site before an analysis.

**Assumptions:**

- Only a stored null counts: an empty selection in a multi-select field is a value, not a null.

**Use something else:**

- [`AGG_COUNT`](#op-agg_count) when you want the rows that do have a value.
- [`FILTER_NULL`](filterer.md#op-filter_null) when you want to keep or drop the rows with no value.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-agg-null-count`](../skills/op-agg-null-count.md)

<a id="op-agg_percentile"></a>

### `AGG_PERCENTILE`

Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time.

**Level:** basic

**Questions it answers:**

- How long do the slowest 10% of deliveries take in each region?
- What income marks the top quarter of respondents?

**Use cases by domain:**

- *survey:* Quartiles of household income per segment.
- *ops:* 95th-percentile response time per service.
- *science:* Reference range (2.5th to 97.5th percentile) of a measurement.

**Assumptions:**

- Missing values are skipped.
- Linear interpolation between the two nearest sorted values, so the result may be a value no row holds.

**Use something else:**

- [`AGG_MEDIAN`](#op-agg_median) when you want the 50th percentile.
- [`ATTR_PERCENTILE`](attribute.md#op-attr_percentile) when you want each row's own percentile position.
- [`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want rows split into equal-sized bands.

**Glossary:** [`percentile`](../glossary.md#term-percentile), [`median`](../glossary.md#term-median), [`outlier`](../glossary.md#term-outlier)

**Skill:** [`op-agg-percentile`](../skills/op-agg-percentile.md)

<a id="op-agg_range"></a>

### `AGG_RANGE`

Distance between the smallest and largest value of a numeric field, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How far apart are the cheapest and most expensive orders?
- How widely do delivery times span in each depot?

**Use cases by domain:**

- *survey:* Span of ages within each segment.
- *ops:* Spread of daily sales between the slowest and busiest store.
- *science:* Span of readings across repeat measurements.

**Assumptions:**

- Missing values are skipped; a group where no row has a value reads 0, not empty, so check n in the components before reporting it.
- It rests on the two most extreme rows, so one outlier can stretch it a lot.

**Use something else:**

- [`AGG_STDDEV`](#op-agg_stddev) when you want a spread measure that uses every row, not only the two extremes.
- [`AGG_PERCENTILE`](#op-agg_percentile) when you want the spread of the middle of the data, ignoring extremes.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`standard-deviation`](../glossary.md#term-standard-deviation)

**Skill:** [`op-agg-range`](../skills/op-agg-range.md)

<a id="op-agg_ratio"></a>

### `AGG_RATIO`

Total of one field divided by the total of another, over all rows or per group, such as revenue per order.

**Level:** basic

**Questions it answers:**

- What is revenue per visit in each channel?
- What share of budgeted hours were actually worked per team?

**Use cases by domain:**

- *survey:* Completes per invitation sent, per wave.
- *ops:* Revenue per order, or cost per unit, by region.
- *science:* Events per person-year of follow-up per arm.

**Assumptions:**

- A row missing either field is left out of both totals.
- Empty (NaN) when the denominator total is 0.

**Use something else:**

- [`ATTR_FORMULA`](attribute.md#op-attr_formula) when you want the average of each row's own ratio: compute it per row (guarding a 0 denominator), then average it.
- [`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) when you want an average where some rows count more than others.

**Glossary:** [`mean`](../glossary.md#term-mean)

**Skill:** [`op-agg-ratio`](../skills/op-agg-ratio.md)

<a id="op-agg_set_cardinality_avg"></a>

### `AGG_SET_CARDINALITY_AVG`

For a multi-select field, the average number of options chosen per row.

**Level:** basic

**Questions it answers:**

- How many brands does a respondent recognise on average?
- How many features does a typical customer have turned on?

**Use cases by domain:**

- *survey:* Average number of answers to a select-all-that-apply question, per segment.
- *ops:* Average number of products per customer bundle.

**Assumptions:**

- Rows with an empty selection count as zero and pull the average down; null rows are skipped.

**Use something else:**

- [`AGG_SET_CARDINALITY_SUM`](#op-agg_set_cardinality_sum) when you want the total number of selections.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want which options were chosen.

**Glossary:** [`mean`](../glossary.md#term-mean), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-agg-set-cardinality-avg`](../skills/op-agg-set-cardinality-avg.md)

<a id="op-agg_set_cardinality_sum"></a>

### `AGG_SET_CARDINALITY_SUM`

For a multi-select field, the total number of options chosen across all rows.

**Level:** basic

**Questions it answers:**

- How many brand mentions did the survey collect in total?
- How many add-ons were sold across all orders?

**Use cases by domain:**

- *survey:* Total mentions for a select-all-that-apply question, as a base for share of mentions.
- *ops:* Total add-ons attached to orders per channel.

**Assumptions:**

- Null rows are skipped; an empty selection adds zero.

**Use something else:**

- [`AGG_SET_CARDINALITY_AVG`](#op-agg_set_cardinality_avg) when you want the number per row.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want the count for each option.

**Skill:** [`op-agg-set-cardinality-sum`](../skills/op-agg-set-cardinality-sum.md)

<a id="op-agg_set_distinct_values"></a>

### `AGG_SET_DISTINCT_VALUES`

For a multi-select field, how many different combinations of options appear.

**Level:** basic

**Questions it answers:**

- How many different combinations of channels do customers use?
- How many different combinations of brands were picked (an empty answer counts as one)?

**Use cases by domain:**

- *survey:* Variety of answer patterns to a select-all-that-apply question.
- *ops:* Number of distinct product bundles sold.

**Assumptions:**

- Each combination is one value: choosing A and B differs from choosing A alone.
- An empty selection counts as one combination; null rows are skipped.

**Use something else:**

- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want the count of each single option.
- [`AGG_SET_UNION`](#op-agg_set_union) when you want every option chosen by anyone.
- [`GROUP_SET_VALUE`](grouper.md#op-group_set_value) when you want how common each combination is: a count alone cannot show whether one dominates.

**Skill:** [`op-agg-set-distinct-values`](../skills/op-agg-set-distinct-values.md)

<a id="op-agg_set_frequency"></a>

### `AGG_SET_FREQUENCY`

For a multi-select field, how many rows chose each option, over all rows or per group.

**Level:** basic

**Questions it answers:**

- How many respondents selected each brand they have heard of?
- How many customers use each payment method?

**Use cases by domain:**

- *survey:* Counts per option of a select-all-that-apply question.
- *ops:* Customers per enabled feature.

**Assumptions:**

- A row with several options is counted once under each, so the counts can add up to more than the rows.

**Use something else:**

- [`GROUP_CATEGORY`](grouper.md#op-group_category) when the field holds one value per row: group by it, then count.
- [`AGG_SET_DISTINCT_VALUES`](#op-agg_set_distinct_values) when you want how many different combinations were chosen.

**Skill:** [`op-agg-set-frequency`](../skills/op-agg-set-frequency.md)

<a id="op-agg_set_intersection"></a>

### `AGG_SET_INTERSECTION`

For a multi-select field, the options chosen by every row, over all rows or per group.

**Level:** basic

**Questions it answers:**

- Which brands did every respondent in this segment recognise?
- Which features does every customer on a plan use?

**Use cases by domain:**

- *survey:* Options recognised by the whole segment.
- *ops:* Features common to every account on a plan.

**Assumptions:**

- Null rows are skipped, but one row with an empty selection empties the result.

**Use something else:**

- [`AGG_SET_UNION`](#op-agg_set_union) when you want every option chosen by anyone.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want how many rows chose each option.

**Skill:** [`op-agg-set-intersection`](../skills/op-agg-set-intersection.md)

<a id="op-agg_set_union"></a>

### `AGG_SET_UNION`

For a multi-select field, every option chosen by at least one row, over all rows or per group.

**Level:** basic

**Questions it answers:**

- Which brands were mentioned by anyone in this segment?
- Which features does at least one customer in each plan use?

**Use cases by domain:**

- *survey:* Options that received any mention per segment.
- *ops:* Features used by anyone on each plan.

**Assumptions:**

- Null rows are skipped.

**Use something else:**

- [`AGG_SET_INTERSECTION`](#op-agg_set_intersection) when you want the options every row chose.
- [`AGG_SET_FREQUENCY`](#op-agg_set_frequency) when you want how many rows chose each option.

**Skill:** [`op-agg-set-union`](../skills/op-agg-set-union.md)

<a id="op-agg_skewness"></a>

### `AGG_SKEWNESS`

How lopsided a numeric field is: positive when a long tail runs to high values, negative when it runs to low ones.

**Level:** intermediate

**Questions it answers:**

- Are incomes in each region bunched low with a few very high ones?
- Is the delivery-time distribution symmetric or dragged out by slow orders?

**Use cases by domain:**

- *survey:* Whether a rating scale piles up at one end per segment.
- *ops:* Whether order values have a long tail of large orders.
- *science:* Checking a measurement's shape before choosing a test.

**Assumptions:**

- Missing values are skipped.
- Population moment coefficient g1 (dividing by n): at small n it runs smaller in size than the adjusted G1 most packages print.
- It is 0 when n is 0 or 1 or every value is the same.

**Use something else:**

- [`AGG_KURTOSIS`](#op-agg_kurtosis) when you want to know whether the tails are heavy rather than lopsided.
- [`TEST_SHAPIRO_WILK`](test.md#op-test_shapiro_wilk) when you want a test of whether the field follows a normal shape.
- [`AGG_MEDIAN`](#op-agg_median) when you want the typical value of a skewed field.

**Glossary:** [`skew`](../glossary.md#term-skew), [`outlier`](../glossary.md#term-outlier), [`median`](../glossary.md#term-median), [`normal-distribution`](../glossary.md#term-normal-distribution)

**Skill:** [`op-agg-skewness`](../skills/op-agg-skewness.md)

<a id="op-agg_stddev"></a>

### `AGG_STDDEV`

Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group.

**Level:** basic

**Also known as:** `standard deviation`, `sd`

**Questions it answers:**

- How much do delivery times vary around the average in each region?
- Which product line's order values vary the most in absolute terms (divide by each line's average to compare relative consistency)?

**Use cases by domain:**

- *survey:* How widely satisfaction scores spread within each segment.
- *ops:* Day-to-day variability of order volume per store.
- *science:* Spread of a measurement within each treatment arm.

**Assumptions:**

- Missing values are skipped.
- Population form: the squared distances are averaged over n, not n - 1.

**Use something else:**

- [`AGG_CI_LOWER`](#op-agg_ci_lower) when you want how precisely the average itself is known.
- [`AGG_WELFORD`](#op-agg_welford) when you want the sample form (dividing by n - 1) together with the mean and n.
- [`AGG_PERCENTILE`](#op-agg_percentile) when the field has extreme values and you want a spread they cannot drag.

**Glossary:** [`standard-deviation`](../glossary.md#term-standard-deviation), [`variance`](../glossary.md#term-variance), [`mean`](../glossary.md#term-mean), [`outlier`](../glossary.md#term-outlier)

**Skill:** [`op-agg-stddev`](../skills/op-agg-stddev.md)

<a id="op-agg_sum"></a>

### `AGG_SUM`

Total of a numeric field, over all rows or per group.

**Level:** basic

**Questions it answers:**

- What is total revenue by region?
- How many units did each product line sell in total?

**Use cases by domain:**

- *survey:* Total of a per-respondent weight to get the weighted base of each segment.
- *ops:* Revenue per sales channel per month.
- *science:* Total dose delivered per treatment arm.

**Assumptions:**

- Missing values are skipped: they add nothing to the total.

**Use something else:**

- [`AGG_AVERAGE`](#op-agg_average) when you want the typical value per row rather than the total.
- [`AGG_WEIGHTED_MEAN`](#op-agg_weighted_mean) when rows carry weights and you want a weighted average.
- [`AGG_DISTINCT_SUM`](#op-agg_distinct_sum) when a value repeats on several rows of the same key and must be added once per key.
- [`WIN_RUNNING_SUM`](window.md#op-win_running_sum) when you want the total so far, row by row.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-agg-sum`](../skills/op-agg-sum.md)

<a id="op-agg_variance"></a>

### `AGG_VARIANCE`

Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group.

**Level:** intermediate

**Questions it answers:**

- How much does the order value vary within each channel, in squared units?
- Which sites show the most variable readings?

**Use cases by domain:**

- *ops:* Variability of daily demand per product, for a safety-stock formula.
- *science:* Variance of a measurement across every unit of a fully measured batch.

**Assumptions:**

- Missing values are skipped.
- Population form: the squared distances are averaged over n, not n - 1.

**Use something else:**

- [`AGG_STDDEV`](#op-agg_stddev) when you want the spread in the field's own units, which is easier to read.
- [`AGG_WELFORD`](#op-agg_welford) when you want the sample variance (dividing by n - 1) with the mean and n, as pooling and study-size planning expect.

**Glossary:** [`variance`](../glossary.md#term-variance), [`standard-deviation`](../glossary.md#term-standard-deviation), [`mean`](../glossary.md#term-mean)

**Skill:** [`op-agg-variance`](../skills/op-agg-variance.md)

<a id="op-agg_weighted_mean"></a>

### `AGG_WEIGHTED_MEAN`

Average of a numeric field where each row counts in proportion to a weight field, such as a survey weight.

**Level:** intermediate

**Questions it answers:**

- What is the weighted average satisfaction per region?
- What is the average price per unit when each order counts by its quantity?

**Use cases by domain:**

- *survey:* Population-weighted mean score per segment.
- *ops:* Volume-weighted average price per product.
- *science:* Precision-weighted mean of repeated measurements.

**Assumptions:**

- Rows missing the value or the weight, or with weight 0, are left out of the average; when no row is left (or the weights add up to 0) it reads 0, not empty, so check sum_weights in the components.
- Negative weights are not refused and can make the result unstable.

**Use something else:**

- [`AGG_AVERAGE`](#op-agg_average) when every row should count equally.
- [`AGG_RATIO`](#op-agg_ratio) when you want one total divided by another.

**Glossary:** [`weighting`](../glossary.md#term-weighting), [`mean`](../glossary.md#term-mean), [`effective-sample-size`](../glossary.md#term-effective-sample-size)

**Skill:** [`op-agg-weighted-mean`](../skills/op-agg-weighted-mean.md)

<a id="op-agg_welford"></a>

### `AGG_WELFORD`

Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs.

**Level:** intermediate

**Questions it answers:**

- What are the mean, variance and size of each cell, so cells can be tested against each other?
- How do the average and spread of scores compare across segments?

**Use cases by domain:**

- *survey:* Per-cell mean score, variance and base feeding a t-test overlay on a crosstab.
- *science:* Per-arm summary statistics for a two-sample comparison.

**Assumptions:**

- Missing values are skipped; only plain integer and float fields are accepted.
- The variance divides by n - 1 and is 0 when fewer than two rows have a value.

**Use something else:**

- [`AGG_AVERAGE`](#op-agg_average) when you want only the average.
- [`AGG_STDDEV`](#op-agg_stddev) when you want the population spread (dividing by n) on its own.
- [`TEST_WELCH`](test.md#op-test_welch) when you want the comparison itself on raw rows.

**Glossary:** [`mean`](../glossary.md#term-mean), [`variance`](../glossary.md#term-variance), [`sample-size`](../glossary.md#term-sample-size), [`t-statistic`](../glossary.md#term-t-statistic)

**Skill:** [`op-agg-welford`](../skills/op-agg-welford.md)

<a id="op-agg_zscore"></a>

### `AGG_ZSCORE`

Population mean and standard deviation of a numeric field, for standardizing it; the value itself is always 0.

**Level:** intermediate

**Questions it answers:**

- What centre and scale standardize this field in each group?
- How many standard deviations does the group's last row (in row order) sit from the group's average, that row included?

**Use cases by domain:**

- *ops:* How far the group's last row, in a fixed row order, sits from the group's centre.
- *science:* Group centre and scale for standardizing a measurement by hand.

**Assumptions:**

- Missing values are skipped; the spread is the population form (dividing by n).
- The last row is the last in row order, not the latest by date, and it is part of the mean and spread it is compared with, which damps its own score; for a reading against its past use OVERLAY_ZSCORE_VS_ROLLING.
- Not streamable: the whole group is read before the result is ready.

**Use something else:**

- [`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want a z-score on every row.
- [`OVERLAY_ZSCORE_VS_TOTAL`](overlay.md#op-overlay_zscore_vs_total) when you want each group's value against all groups.
- [`AGG_STDDEV`](#op-agg_stddev) when you want only the spread.

**Glossary:** [`z-score`](../glossary.md#term-z-score), [`mean`](../glossary.md#term-mean), [`standard-deviation`](../glossary.md#term-standard-deviation)

**Skill:** [`op-agg-zscore`](../skills/op-agg-zscore.md)
