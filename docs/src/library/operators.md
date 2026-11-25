# Choosing an Operator

**Audience:** anyone writing a request who knows the question but not
the operator name.

This page maps common questions to operators. It is not the full list:
every registered operator, with its params, accepted field types and
streamability, is in the [Operator catalog](../guide/catalog.md), in
each operator's skill (`op-<kind>`, through `pulse_skills_get`) and in
the manifest (`pulse manifest --json`). Set-field operators are on
[Field Types → Sets](../format/field-types.md#sets-multi-select-bitmasks).

## Aggregators

| You want | Use |
|---|---|
| Count, total, mean, extremes | `AGG_COUNT`, `AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX` |
| Spread | `AGG_STDDEV`, `AGG_VARIANCE` (population by default; `params.ddof: 1` for the sample figure), `AGG_RANGE` (max − min) |
| Shape | `AGG_SKEWNESS` (g1) and `AGG_KURTOSIS` (excess g2), population moments |
| Median or a percentile | `AGG_MEDIAN`, `AGG_PERCENTILE`; both buffer every value |
| Prediction error | `AGG_MAE`, `AGG_RMSE`, `AGG_MAPE` (a fraction; zero actuals are excluded and counted). `field` is the actual, `params.prediction_field` the prediction, typically an `ATTR_REG_PREDICT` column |

## Groupers

| You want | Use |
|---|---|
| One group per category | `GROUP_CATEGORY` |
| Fixed-width numeric bins | `GROUP_RANGE` |
| Equal-population bins | `GROUP_QUANTILE` |
| Named numeric bands (score bands, a train / holdout split) | `GROUP_NUMERIC_RANGES`: inline `{label, start, end}` ranges or a registered range table (`pulse_range_tables` lists them); labels are unique within a set (`PULSE_RANGE_DUPLICATE_LABEL`) |
| Calendar periods or named date windows | `GROUP_DATE`, `GROUP_DATE_RANGES` |

## Windows

Windows run on the result rows, in each partition's order (`order_by`).

| You want | Use |
|---|---|
| The previous or next row's value | `WIN_LAG`, `WIN_LEAD` |
| Change against an earlier row | `WIN_DELTA` (difference), `WIN_PCT_CHANGE` (fraction, 0.05 = 5%) |
| Position in the partition | `WIN_ROW_NUMBER`, `WIN_RANK` (gaps after ties), `WIN_DENSE_RANK` (no gaps) |
| Cumulative figures | `WIN_RUNNING_SUM`, `WIN_RUNNING_AVG` |
| Smoothing | `WIN_MOVING_AVG` (a frame bounded at both ends), `WIN_EWMA` |
| Share of the partition, capture and gains curves | `WIN_SHARE`, `WIN_LIFT` (share of the field over share of a base column) |
| Carry-over, such as media adstock | `WIN_DISTRIBUTED_LAG` |

## Features

| You want | Use |
|---|---|
| Tame skew, add curvature | `FEAT_LOG`, `FEAT_SQRT`, `FEAT_POLY` |
| Encode a categorical as numbers | `FEAT_ONE_HOT` (a column per level), `FEAT_FREQUENCY_ENCODE` (each level's relative frequency; reads no target), `FEAT_TARGET_ENCODE` (the target's smoothed mean; fit it on training rows only) |
| Bin, date parts, a split column | `FEAT_BUCKETIZE`, `FEAT_DATE_FEATURES`, `FEAT_TRAIN_TEST_SPLIT` |

## Statistical tests

| Question | Use |
|---|---|
| Do two groups differ in mean? | `TEST_T` / `TEST_WELCH` (Welch t), `TEST_Z_TWO_SAMPLE` (large samples, normal tail), `TEST_MANN_WHITNEY_U` (ranks) |
| Do paired measurements differ? (`field` and `field2` on one row) | `TEST_PAIRED_T`, `TEST_WILCOXON_SR` (signed ranks) |
| Do k groups differ in mean? | `TEST_ANOVA_F`, `TEST_ANOVA_WELCH` (unequal variances), `TEST_KRUSKAL_WALLIS` (ranks), then `TEST_TUKEY_HSD` for the pairs (k ≥ 3, else `PULSE_TEST_TUKEY_REQUIRES_K_GE_3`); `TEST_ANOVA_RM` for repeated measures |
| Do groups differ in variance? | `TEST_BROWN_FORSYTHE` |
| Are two categorical fields associated? | `TEST_CHISQ`; `TEST_FISHER_EXACT` for a 2 × 2 table with small counts (a larger table is `PULSE_TEST_FISHER_R_OR_C_GT_2`) |
| Do two proportions differ? | `TEST_PROP_Z` |
| Are two numeric fields correlated? | `TEST_PEARSON_R`, `TEST_SPEARMAN_R`, `TEST_KENDALL_TAU` |

Data can leave a test without an answer or with fewer rows than it was
given; the run says so. A correlation over a constant column is
`PULSE_TEST_CORRELATION_UNDEFINED`. A paired row with one side null is
dropped and counted (`PULSE_TEST_PAIRED_LENGTH_MISMATCH`), and so is a
repeated-measures subject missing a condition
(`PULSE_TEST_SUBJECT_MISSING`). A `type` no registry knows is
`PULSE_TEST_UNKNOWN_TYPE`.

Several tests in one request can share a correction:
[Multiple-Comparison Correction](multiplicity.md). For comparisons drawn
on a finished table (a crosstab's columns, a series' prior period), use
an [overlay](overlays.md) instead.
