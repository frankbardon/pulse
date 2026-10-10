# Reading the components

<!-- docgen:preserve begin components-intro -->
Every figure Pulse reports rests on a count of the rows behind it, and `Response.Components` carries those counts beside the result so you can judge how much weight a figure can bear.

**What `n_null` means for your conclusions.** `n` counts the rows an operator actually used; `n_null` counts the rows it skipped because the value was missing. A figure describes the `n` rows only, never the full cohort. When `n_null` is a large share of `n + n_null`, ask why the values are missing before you generalise: if the missing rows differ from the rest (non-response that tracks the outcome, a sensor that fails under load), the figure is biased toward the rows that answered, and no amount of extra data fixes that. Report `n` with every figure, compare `n_null` across groups before comparing the groups themselves, and treat a group whose `n` is small as a rough indication rather than an estimate.
<!-- docgen:preserve end components-intro -->

<a id="floor"></a>

## The floor every entry carries

Pulse fills these keys on every entry of a slot, ahead of the operator's own keys.

### Aggregators

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `n` | int | always | Number of records aggregated (non-null inputs). |
| `n_null` | int | always | Number of null inputs encountered. |

### Groupers

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `total_n` | int | always | Total records partitioned across all buckets (post-filter). |
| `n_null` | int | always | Records that landed in the null/skip path (no bucket assignment). |

### Filterers

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `n_in` | int | always | Records that entered this filter pass. |
| `n_out` | int | always | Records that passed (entered the downstream stage). |
| `n_null_input` | int | always | Records where the filter input field was null at evaluation time. |

### Matrix operators

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `n` | int | always | Rows the co-moment counted: listwise the complete rows, pairwise the rows with any member present; weight-0 rows count. |
| `n_null` | int | always | Rows skipped for missing members: listwise any member null, pairwise every member null. |
| `n_listwise_dropped` | int | always | Rows listwise deletion dropped; 0 under pairwise. |
| `min_pair_n` | int | when its condition holds | Pairwise only: the smallest pair N (the minimum of auxiliary.n). |
| `max_pair_n` | int | when its condition holds | Pairwise only: the largest pair N (the maximum of auxiliary.n). |

<a id="weighted-floor"></a>

## Weighted floor keys

A slot run under a row weight adds these keys beside its floor; their absence means the slot was unweighted. `n` and `n_null` stay raw row counts under a weight.

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum_weights` | float64 | when its condition holds | Weighted slots only: the sum of the valid weights of the rows whose value was present. |
| `n_eff` | float64 | when its condition holds | Weighted slots of kind probability only: Kish effective sample size (sum of weights)^2 / sum of squared weights. |
| `n_weight_invalid` | int | when its condition holds | Weighted slots only: rows with a present value whose weight was invalid (null, negative, NaN/Inf, non-integer frequency) and was excluded. |

<a id="mergeability"></a>

## Mergeability

Each operator's components carry a mergeability class, which says when a streamed run can report them:

- **mergeable**: the components fold chunk by chunk, so a streamed run reports running values as it goes.
- **partial**: the components fold across chunks, but the fold grows with the data (a set or map union), so Pulse may stage it until the end of the run.
- **none**: the components need the whole input at once (a sorted view), so a streamed run reports them only at the end.

## Operator components

Each operator's own keys, beyond the floor and the weighted floor keys.

### Aggregators

<a id="op-agg_average"></a>

#### `AGG_AVERAGE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_average).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum` | float64 | always | Running sum of non-null field values; combined with n to recover the mean (the weighted sum Σw·x on a weighted slot). |
| `sum_weighted` | float64 | when its condition holds | Weighted slots only: Σw·x over the contributing rows. |
| `weighted_mean` | float64 | when its condition holds | Weighted slots only: Σw·x / Σw (the scalar). |
| `m2_weighted` | float64 | when its condition holds | Weighted slots only: Σw(x − mean)², the weighted second central moment. |
| `sum_weights_sq` | float64 | when its condition holds | Weighted slots only: Σw², the Kish n_eff denominator. |
| `weighted_variance` | float64 | when its condition holds | Weighted slots only: m2_weighted / (Σw − 1), the frequency-weights sample variance; 0 when Σw ≤ 1. |

<a id="op-agg_ci_lower"></a>

#### `AGG_CI_LOWER`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_ci_lower).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Welford-running mean of the field. |
| `stderr` | float64 | always | Standard error of the mean derived from variance and n. |
| `alpha` | float64 | always | Significance level (1 - confidence). |
| `t_critical` | float64 | always | Critical value used to scale the standard error. |
| `lower` | float64 | always | Resolved lower bound of the confidence interval. |

<a id="op-agg_ci_upper"></a>

#### `AGG_CI_UPPER`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_ci_upper).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Welford-running mean of the field. |
| `stderr` | float64 | always | Standard error of the mean derived from variance and n. |
| `alpha` | float64 | always | Significance level (1 - confidence). |
| `t_critical` | float64 | always | Critical value used to scale the standard error. |
| `upper` | float64 | always | Resolved upper bound of the confidence interval. |

<a id="op-agg_count"></a>

#### `AGG_COUNT`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_count).

Only the floor keys.

<a id="op-agg_distinct_count"></a>

#### `AGG_DISTINCT_COUNT`

**Mergeability:** partial. See its [catalog entry](../catalog/aggregator.md#op-agg_distinct_count).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `cardinality` | int | always | Number of distinct non-null values observed. |

<a id="op-agg_distinct_sum"></a>

#### `AGG_DISTINCT_SUM`

**Mergeability:** partial. See its [catalog entry](../catalog/aggregator.md#op-agg_distinct_sum).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum` | float64 | always | Sum of the first value observed for each distinct key. |
| `distinct_count` | int | always | Number of distinct keys that contributed to the sum. |

<a id="op-agg_frequency"></a>

#### `AGG_FREQUENCY`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_frequency).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `match_count` | int | always | Non-null rows equal to params.value (= the scalar); on a weighted slot their sum of weights (a float). |
| `share` | float64 | always | match_count / n; omitted when n is 0. On a weighted slot the matching sum of weights over the sum of weights of the non-null rows (omitted when that is 0). |

<a id="op-agg_kurtosis"></a>

#### `AGG_KURTOSIS`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_kurtosis).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Running mean of non-null field values. |
| `m2` | float64 | always | Second-moment accumulator (sum of squared deviations from the running mean). |
| `m3` | float64 | always | Third-moment accumulator (sum of cubed deviations from the running mean). |
| `m4` | float64 | always | Fourth-moment accumulator (sum of fourth-power deviations from the running mean). |
| `kurtosis` | float64 | always | Population excess kurtosis g2 = m4 / (n * variance^2) - 3 from m2, m4 and n (not the small-sample-adjusted G2); 0 when n <= 1 or the variance is zero. Weighted: the moments are Σw-weighted and Σw replaces n. |

<a id="op-agg_max"></a>

#### `AGG_MAX`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_max).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `max` | float64 | always | Largest non-null value observed. |

<a id="op-agg_median"></a>

#### `AGG_MEDIAN`

**Mergeability:** none. See its [catalog entry](../catalog/aggregator.md#op-agg_median).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `position_low` | int | always | Lower index used to bracket the median in the sorted value set (weighted: the expanded-index floor of 0.5·(Σw − 1), probability weights rescaled to Σw = n). |
| `position_high` | int | always | Upper index used to bracket the median in the sorted value set (weighted: the expanded-index ceiling of 0.5·(Σw − 1), probability weights rescaled to Σw = n). |
| `median` | float64 | always | Resolved median value (linear interpolation between the bracketing positions). |

<a id="op-agg_min"></a>

#### `AGG_MIN`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_min).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `min` | float64 | always | Smallest non-null value observed. |

<a id="op-agg_mode"></a>

#### `AGG_MODE`

**Mergeability:** partial. See its [catalog entry](../catalog/aggregator.md#op-agg_mode).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `value` | any | always | Most-frequent value observed (smallest-value tie-break). |
| `count` | int | always | Row count of the modal value; on a weighted slot its sum of weights (a float). |
| `distinct_count` | int | always | Number of distinct values observed. |
| `tie_count` | int | always | Number of values tied with the mode at the same maximum count. |

<a id="op-agg_mode_count"></a>

#### `AGG_MODE_COUNT`

**Mergeability:** partial. See its [catalog entry](../catalog/aggregator.md#op-agg_mode_count).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `distinct_count` | int | always | Number of distinct values observed. |
| `mode_value` | any | always | Most-frequent value — the largest sum of weights on a weighted slot (ties broken by the smallest value, matching AGG_MODE). |
| `mode_count` | int | always | Row count of the modal value; on a weighted slot its sum of weights (a float). |

<a id="op-agg_null_count"></a>

#### `AGG_NULL_COUNT`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_null_count).

Only the floor keys.

<a id="op-agg_percentile"></a>

#### `AGG_PERCENTILE`

**Mergeability:** none. See its [catalog entry](../catalog/aggregator.md#op-agg_percentile).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `p` | float64 | always | Percentile requested, in [0, 100]. |
| `position` | int | always | Index into the sorted value set used to resolve the percentile (weighted: the expanded-index floor of p·(Σw − 1), probability weights rescaled to Σw = n). |
| `lower` | float64 | always | Lower bracketing value used during interpolation. |
| `upper` | float64 | always | Upper bracketing value used during interpolation. |
| `method` | string | always | Interpolation method (e.g. "linear"). |
| `value` | float64 | always | Resolved percentile value. |

<a id="op-agg_range"></a>

#### `AGG_RANGE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_range).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `min` | float64 | always | Smallest non-null value observed. |
| `max` | float64 | always | Largest non-null value observed. |

<a id="op-agg_ratio"></a>

#### `AGG_RATIO`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_ratio).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `numerator` | float64 | always | Running sum of the numerator field (weighted slot: of weight * numerator). |
| `denominator` | float64 | always | Running sum of the denominator field (weighted slot: of weight * denominator). |
| `ratio` | float64 | always | Resolved ratio: numerator / denominator (NaN, null in JSON, when denominator is zero). |

<a id="op-agg_set_cardinality_avg"></a>

#### `AGG_SET_CARDINALITY_AVG`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_set_cardinality_avg).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum_cardinality` | int | always | Sum of popcounts across contributing rows; weighted slot: sum(w * popcount) (a float). |
| `avg_cardinality` | float64 | always | Average popcount per contributing row (sum_cardinality / n; weighted slot: / sum of weights). |

<a id="op-agg_set_cardinality_sum"></a>

#### `AGG_SET_CARDINALITY_SUM`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_set_cardinality_sum).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum_cardinality` | int | always | Sum of popcounts across contributing rows — total label selections seen; weighted slot: sum(w * popcount) (a float). |

<a id="op-agg_set_distinct_values"></a>

#### `AGG_SET_DISTINCT_VALUES`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_set_distinct_values).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mask_union` | []uint64 | always | Bitwise OR of every contributing row's set mask, as four little-endian 64-bit words (words[0] = bits 0-63). Fixed length at every set rung, so a 256-bit mask is carried whole and a consumer indexes words[bit/64] without knowing the column width. |
| `popcount` | int | always | Number of bits set in mask_union (count of distinct labels observed). |
| `labels` | []string | always | Resolved dictionary labels for every bit set in mask_union. |

<a id="op-agg_set_frequency"></a>

#### `AGG_SET_FREQUENCY`

**Mergeability:** partial. See its [catalog entry](../catalog/aggregator.md#op-agg_set_frequency).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `total_label_observations` | int | always | Sum of popcounts across contributing rows (total label selections seen); weighted slot: the weight-summed selections (a float). |
| `distinct_labels` | int | always | Number of distinct labels observed at least once (weighted slot: with a positive weight sum). |
| `per_label_count` | map[string]int | always | Per-label row count: how many rows had each label selected; weighted slot: each label's sum of weights (float values). |

<a id="op-agg_set_intersection"></a>

#### `AGG_SET_INTERSECTION`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_set_intersection).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mask_intersection` | []uint64 | always | Bitwise AND of every contributing row's set mask, as four little-endian 64-bit words (words[0] = bits 0-63). Fixed length at every set rung, so a 256-bit mask is carried whole and a consumer indexes words[bit/64] without knowing the column width. |
| `popcount` | int | always | Number of bits set in mask_intersection. |
| `labels` | []string | always | Resolved dictionary labels for every bit set in mask_intersection. |

<a id="op-agg_set_union"></a>

#### `AGG_SET_UNION`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_set_union).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mask_union` | []uint64 | always | Bitwise OR of every contributing row's set mask, as four little-endian 64-bit words (words[0] = bits 0-63). Fixed length at every set rung, so a 256-bit mask is carried whole and a consumer indexes words[bit/64] without knowing the column width. |
| `popcount` | int | always | Number of bits set in mask_union. |
| `labels` | []string | always | Resolved dictionary labels for every bit set in mask_union. |

<a id="op-agg_skewness"></a>

#### `AGG_SKEWNESS`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_skewness).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Running mean of non-null field values. |
| `m2` | float64 | always | Second-moment accumulator (sum of squared deviations from the running mean). |
| `m3` | float64 | always | Third-moment accumulator (sum of cubed deviations from the running mean). |
| `skewness` | float64 | always | Population skewness g1 = m3 / (n * sd^3) from m2, m3 and n (not the small-sample-adjusted G1); 0 when n <= 1 or the variance is zero. Weighted: the moments are Σw-weighted and Σw replaces n. |

<a id="op-agg_stddev"></a>

#### `AGG_STDDEV`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_stddev).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Running Welford mean of non-null field values. |
| `m2` | float64 | always | Welford second-moment accumulator (sum of squared deviations from the running mean). |
| `variance` | float64 | always | Population variance derived from m2 / n. |
| `stddev` | float64 | always | Population standard deviation (square root of variance). |

<a id="op-agg_sum"></a>

#### `AGG_SUM`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_sum).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum` | float64 | always | Running sum of non-null field values. |

<a id="op-agg_variance"></a>

#### `AGG_VARIANCE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_variance).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Running Welford mean of non-null field values. |
| `m2` | float64 | always | Welford second-moment accumulator (sum of squared deviations from the running mean). |
| `variance` | float64 | always | Population variance derived from m2 / n. |

<a id="op-agg_weighted_mean"></a>

#### `AGG_WEIGHTED_MEAN`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_weighted_mean).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `sum_weighted` | float64 | always | Running sum of (field * weight) across contributing rows. |
| `weighted_mean` | float64 | always | Resolved weighted mean: sum_weighted / sum_weights. |
| `m2_weighted` | float64 | always | Weighted second central moment: sum(weight * (field - weighted_mean)^2). |
| `sum_weights_sq` | float64 | always | Running sum of squared weights; feeds the Kish effective sample size. |
| `weighted_variance` | float64 | always | Frequency-weights variance m2_weighted / (sum_weights - 1); 0 when sum_weights <= 1. |

<a id="op-agg_welford"></a>

#### `AGG_WELFORD`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_welford).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `mean` | float64 | always | Running Welford mean of non-null field values. |
| `m2` | float64 | always | Welford second-moment accumulator (sum of squared deviations from the running mean). |
| `variance` | float64 | always | Unbiased sample variance derived from m2 / (n-1). |
| `stddev` | float64 | always | Sample standard deviation (square root of variance). |

<a id="op-agg_zscore"></a>

#### `AGG_ZSCORE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/aggregator.md#op-agg_zscore).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `pop_mean` | float64 | always | Population mean used as the z-score center. |
| `pop_stddev` | float64 | always | Population standard deviation used as the z-score scale. |
| `target_value` | float64 | always | Value being standardized against the population summary. |
| `zscore` | float64 | always | Standardized score: (target_value - pop_mean) / pop_stddev. |

### Groupers

<a id="op-group_category"></a>

#### `GROUP_CATEGORY`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_category).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `dict_size` | int | always | Number of distinct values observed across all buckets (cardinality of the partition key). |
| `buckets` | []bucket | always | Per-bucket records, ordered by emission; each entry carries {key, label, count}. |

<a id="op-group_date"></a>

#### `GROUP_DATE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_date).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `granularity` | string | always | Calendar component used to bucket (hour, day, day_of_week, week, month, quarter, year). |
| `range_start` | string | always | ISO date string of the earliest period observed (YYYY-MM-DDTHH for component=hour). |
| `range_end` | string | always | ISO date string of the latest period observed (YYYY-MM-DDTHH for component=hour). |
| `n_buckets` | int | always | Number of distinct calendar buckets observed. |
| `buckets` | []bucket | always | Per-bucket records, ordered by emission; each entry carries {key, period_start, period_end, count}. |

<a id="op-group_date_ranges"></a>

#### `GROUP_DATE_RANGES`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_date_ranges).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `n_ranges` | int | always | Number of configured labeled date ranges. |
| `unmatched_label` | string | always | Bucket label used for rows outside every configured range. |
| `buckets` | []bucket | always | Per-bucket records in supplied range order (unmatched last when observed); each entry carries {key, label, count}. |

<a id="op-group_quantile"></a>

#### `GROUP_QUANTILE`

**Mergeability:** none. See its [catalog entry](../catalog/grouper.md#op-group_quantile).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `n_quantiles` | int | always | Number of equal-population quantile buckets requested (Group.Interval). |
| `method` | string | always | Quantile interpolation method (e.g. "linear"). |
| `edges` | []float64 | always | Sorted cutpoints separating adjacent quantile buckets. |
| `buckets` | []bucket | always | Per-bucket records, ordered by emission; each entry carries {key, low, high, count}. |

<a id="op-group_range"></a>

#### `GROUP_RANGE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_range).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `interval` | float64 | always | Bucket width on the value axis (Group.Interval). |
| `range_min` | float64 | always | Smallest non-null value observed across all buckets. |
| `range_max` | float64 | always | Largest non-null value observed across all buckets. |
| `n_buckets` | int | always | Number of half-open range buckets emitted. |
| `edges` | []float64 | always | Sorted cutpoints separating adjacent range buckets. |
| `buckets` | []bucket | always | Per-bucket records, ordered by emission; each entry carries {key, low, high, count}. |
| `underflow_count` | int | always | Records below the lowest configured bucket edge. |
| `overflow_count` | int | always | Records at or above the highest configured bucket edge. |

<a id="op-group_rounded"></a>

#### `GROUP_ROUNDED`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_rounded).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `precision` | float64 | always | Rounding increment used as the bucket scalar (Group.Interval). |
| `edges` | []float64 | always | Sorted rounded scalars separating adjacent buckets. |
| `buckets` | []bucket | always | Per-bucket records, ordered by emission; each entry carries {key, low, high, count}. |

<a id="op-group_set_per_element"></a>

#### `GROUP_SET_PER_ELEMENT`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_set_per_element).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `total_label_observations` | int | always | Sum of buckets[].count across the partition; may exceed total_n because each row fans into one bucket per selected label. |
| `buckets` | []bucket | always | Per-label records, ordered by emission; each entry carries {key, label, count, dict_index}. |

<a id="op-group_set_value"></a>

#### `GROUP_SET_VALUE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/grouper.md#op-group_set_value).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `n_empty_mask` | int | always | Records whose set mask was the empty selection (zero-bit mask is a valid distinct bucket from null). |
| `buckets` | []bucket | always | Per-bucket records, ordered by emission; each entry carries {key, count, labels} plus exactly one of mask (uint64, when the selection fits 64 bits) or mask_words ([]uint64, little-endian low word first, when a set_u128/set_u256 selection reaches bit 64 or above). The two are mutually exclusive: a uint64 mask carrying only the low word of a wide selection would be a plausible wrong number. |

### Filterers

<a id="op-filter_date_ranges"></a>

#### `FILTER_DATE_RANGES`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_date_ranges).

Only the floor keys.

<a id="op-filter_exclude"></a>

#### `FILTER_EXCLUDE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_exclude).

Only the floor keys.

<a id="op-filter_expression"></a>

#### `FILTER_EXPRESSION`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_expression).

Only the floor keys.

<a id="op-filter_false"></a>

#### `FILTER_FALSE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_false).

Only the floor keys.

<a id="op-filter_include"></a>

#### `FILTER_INCLUDE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_include).

Only the floor keys.

<a id="op-filter_null"></a>

#### `FILTER_NULL`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_null).

Only the floor keys.

<a id="op-filter_range"></a>

#### `FILTER_RANGE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_range).

Only the floor keys.

<a id="op-filter_set_contains_all"></a>

#### `FILTER_SET_CONTAINS_ALL`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_set_contains_all).

Only the floor keys.

<a id="op-filter_set_contains_any"></a>

#### `FILTER_SET_CONTAINS_ANY`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_set_contains_any).

Only the floor keys.

<a id="op-filter_set_contains_none"></a>

#### `FILTER_SET_CONTAINS_NONE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_set_contains_none).

Only the floor keys.

<a id="op-filter_set_equals"></a>

#### `FILTER_SET_EQUALS`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_set_equals).

Only the floor keys.

<a id="op-filter_true"></a>

#### `FILTER_TRUE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/filterer.md#op-filter_true).

Only the floor keys.

### Matrix operators

<a id="op-mat_correlation"></a>

#### `MAT_CORRELATION`

**Mergeability:** mergeable. See its [catalog entry](../catalog/matrix.md#op-mat_correlation).

Only the floor keys.

<a id="op-mat_covariance"></a>

#### `MAT_COVARIANCE`

**Mergeability:** mergeable. See its [catalog entry](../catalog/matrix.md#op-mat_covariance).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `ddof` | int | always | The delta degrees of freedom the covariance used (params.ddof, default 1). |

<a id="op-mat_partial_correlation"></a>

#### `MAT_PARTIAL_CORRELATION`

**Mergeability:** mergeable. See its [catalog entry](../catalog/matrix.md#op-mat_partial_correlation).

Only the floor keys.

<a id="op-mat_pca"></a>

#### `MAT_PCA`

**Mergeability:** mergeable. See its [catalog entry](../catalog/matrix.md#op-mat_pca).

Only the floor keys.

<a id="op-mat_reliability"></a>

#### `MAT_RELIABILITY`

**Mergeability:** mergeable. See its [catalog entry](../catalog/matrix.md#op-mat_reliability).

| Key | Type | Emitted | Meaning |
|---|---|---|---|
| `iterations` | int | when its condition holds | Coordinate sweeps the one-factor minres fit behind omega ran (cap 1000); absent when no fit ran (2 items, an undefined or unrepaired non-PSD input). |
| `converged` | bool | when its condition holds | Whether the minres fit met its tolerance (1e-12) within the cap; false comes with PULSE_MATRIX_NOT_CONVERGED. Absent when no fit ran. |
