# Change over time

## The question

Questions of this kind sound like:

- Is it going up?
- How does this month compare to last?

## Pick your analysis

This question fits data shaped like:

- `time`: one date field; `measure`: 1 or more numeric, categorical or bool fields

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_DATE_PART`](../catalog/attribute.md#op-attr_date_part) | Adds a calendar part of a date or timestamp to every row, such as the year, the month, a year-month like 202403 or the hour. | basic |

### Filterers

| Use | In plain words | Level |
|---|---|---|
| [`FILTER_DATE_RANGES`](../catalog/filterer.md#op-filter_date_ranges) | Keeps only the rows whose date falls inside one of a set of named date ranges, such as custom fiscal quarters. | basic |

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_DATE`](../catalog/grouper.md#op-group_date) | Splits the rows by calendar period of a date, such as month, quarter, ISO week or weekday, to see a measure over time. | basic |
| [`GROUP_DATE_RANGES`](../catalog/grouper.md#op-group_date_ranges) | Labels each row with the named date range its date falls in, such as custom fiscal quarters or before and after a launch. | basic |

### Window operators

| Use | In plain words | Level |
|---|---|---|
| [`WIN_DELTA`](../catalog/window.md#op-win_delta) | Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month. | basic |
| [`WIN_LAG`](../catalog/window.md#op-win_lag) | Adds to every row the value of a field from a set number of rows earlier in the ordered series, such as last month's sales. | basic |
| [`WIN_LEAD`](../catalog/window.md#op-win_lead) | Adds to every row the value of a field from a set number of rows later in the ordered series, such as next month's sales. | basic |
| [`WIN_MOVING_AVG`](../catalog/window.md#op-win_moving_avg) | Adds a moving average: the mean of a field over a fixed number of neighbouring rows, smoothing short-term swings in a series. | basic |
| [`WIN_PCT_CHANGE`](../catalog/window.md#op-win_pct_change) | Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month. | basic |
| [`WIN_RUNNING_AVG`](../catalog/window.md#op-win_running_avg) | Adds the average of a field over the rows so far in the ordered series, such as the average order value to date. | basic |
| [`WIN_RUNNING_SUM`](../catalog/window.md#op-win_running_sum) | Adds a running total of a field down the ordered rows, such as revenue for the year to date. | basic |
| [`WIN_EWMA`](../catalog/window.md#op-win_ewma) | Adds a smoothed series: each new value counts for a fixed share w, the previous smoothed level for 1 - w, so older values fade. | intermediate |

### Feature operators

| Use | In plain words | Level |
|---|---|---|
| [`FEAT_DATE_FEATURES`](../catalog/feature.md#op-feat_date_features) | Splits a date into year, month, day, day of week and quarter columns, so each part can be filtered, grouped or modelled. | basic |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_PAIRED_T`](../catalog/test.md#op-test_paired_t) | Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero. | intermediate |
| [`TEST_TREND`](../catalog/test.md#op-test_trend) | Checks whether an ordered series, such as monthly totals, tends to keep rising or keep falling. | intermediate |
| [`TEST_WILCOXON_SR`](../catalog/test.md#op-test_wilcoxon_sr) | Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve. | intermediate |
| [`TEST_ANOVA_RM`](../catalog/test.md#op-test_anova_rm) | Checks whether the average differs across conditions when every subject is measured under each condition. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_DELTA_VS_BASELINE`](../catalog/overlay.md#op-overlay_delta_vs_baseline) | Each point of an ordered series minus a chosen baseline point, in the value's own units: how much it has moved since then. | basic |
| [`OVERLAY_DELTA_VS_PRIOR`](../catalog/overlay.md#op-overlay_delta_vs_prior) | Each point of an ordered series minus the point before it, in the value's own units: how much it changed from the previous period. | basic |
| [`OVERLAY_INDEX_VS_BASELINE`](../catalog/overlay.md#op-overlay_index_vs_baseline) | Each point of an ordered series as an index value against a chosen baseline point (point / baseline x 100), e.g. growth since launch. | basic |
| [`OVERLAY_INDEX_VS_PRIOR`](../catalog/overlay.md#op-overlay_index_vs_prior) | Each point of an ordered series as an index value against the point before it (point / prior x 100): period-on-period change. | basic |
| [`OVERLAY_YOY`](../catalog/overlay.md#op-overlay_yoy) | Each period of a date-grouped series as an index value against the same period one year earlier (x 100): year-over-year change. | basic |
| [`OVERLAY_INDEX_VS_ROLLING_MEAN`](../catalog/overlay.md#op-overlay_index_vs_rolling_mean) | Each point of an ordered series as an index value against the rolling mean of the previous W points: is this period above its recent run? | intermediate |
| [`OVERLAY_PROP_Z_VS_PRIOR`](../catalog/overlay.md#op-overlay_prop_z_vs_prior) | Two-proportion z-test of each period's share against the period before it: did awareness (or any yes/no share) change since last wave? | intermediate |
| [`OVERLAY_ZSCORE_VS_ROLLING`](../catalog/overlay.md#op-overlay_zscore_vs_rolling) | How many standard deviations each point sits from the rolling mean of the previous W points: a simple flag for unusual periods. | intermediate |
| [`OVERLAY_T_VS_PRIOR`](../catalog/overlay.md#op-overlay_t_vs_prior) | Welch t-test of each period against the period before it: did the average really change from the last wave, or is it noise? | advanced |
| [`OVERLAY_Z_VS_PRIOR`](../catalog/overlay.md#op-overlay_z_vs_prior) | Large-sample z-test of each period's mean against the period before it: did the average change from the last wave? | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent change_over_time` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Compare groups](compare_groups.md)
- [Describe a measure](describe.md)
- [Benchmark against a reference](benchmark.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`statistical-testing`](../skills/statistical-testing.md), [`window-design`](../skills/window-design.md)

**From the command line:**

- `pulse recommend --intent change_over_time --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
