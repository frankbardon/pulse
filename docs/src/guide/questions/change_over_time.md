# Change over time

## The question

<!-- docgen:preserve begin guide-change_over_time-question -->
You have a measure recorded over time — orders per day, revenue per month, the same customers before and after a change — and you want to know whether it is going up or down, how this period compares to the last one, or whether a change after some event is more than noise.

It sounds like:

- "Is revenue going up?"
- "How does this month compare to last month, and to the same month last year?"
- "Did customers spend more after the launch than before it?"

There are three common shapes. Bucket the rows by period (day, week, month) and read the series, adding period-over-period changes or indexes. Test the whole series for a steady upward or downward trend. Or, when the same rows are measured twice, compare each row with itself before and after.
<!-- docgen:preserve end guide-change_over_time-question -->

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

**Did customers spend more during the test period than they did before it?**

The request (example `paired_t_before_after`; `pulse examples show paired_t_before_after` prints it):

```json
{
  "cohort": {
    "filename": "experiment.pulse",
    "data_dir": ".data"
  },
  "return": {
    "precision": 4
  },
  "tests": [
    {
      "type": "TEST_PAIRED_T",
      "field": "revenue",
      "field2": "revenue_before",
      "alpha": 0.05,
      "label": "revenue_lift_paired"
    }
  ]
}
```

The result:

```json
{
  "metadata": {
    "total_rows": 400,
    "filtered_rows": 400,
    "cohort_file": "experiment.pulse"
  },
  "tests": [
    {
      "label": "revenue_lift_paired",
      "type": "TEST_PAIRED_T",
      "variant": "paired_two_sided",
      "statistic": 23.95,
      "df": 399,
      "p_value": 3.227e-79,
      "alpha": 0.05,
      "reject_null": true,
      "details": {
        "ci_high": 33.43,
        "ci_low": 28.36,
        "effect_size": {
          "cohens_d": 1.198
        },
        "mean_diff": 30.9,
        "n": 400,
        "variance": 665.6
      }
    }
  ],
  "components": {
    "run": {
      "total_records": 400,
      "filtered_records": 400,
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

Yes, clearly. Comparing each customer with themselves, revenue rose on average by roughly a quarter of the earlier level; the interval for that rise is narrow and well above zero, and the effect size is large. Pairing is what makes this sharp: each customer is their own baseline, so the wide differences between customers drop out. The test says spending rose, not why: the test period mixes the treatment with a gradual drift over time, so the rise belongs to both.

## How to read the result

### `TEST_PAIRED_T`

**`statistic`**

t is the average of the per-row differences (Field - Field2) measured in standard errors of that average. Values far from 0 in either direction are unlikely if the true average difference is zero.

**Sign:**

- `+`: Field is larger than Field2 on average
- `-`: Field2 is larger than Field on average

**`p_value`**

Follows the shared reading: see [Reading a p-value](../reading/test.md#shared-p-value).

**`details.mean_diff`**

The average of Field - Field2 over complete pairs, in the field's own units.

**Sign:**

- `+`: Field is larger than Field2 on average
- `-`: Field2 is larger than Field on average

**`details.ci_low`**

Lower end of the confidence interval for the average difference (Field - Field2) at the 1 - alpha level (95% by default).

**Caveats:**

- An interval that excludes 0 matches a p-value below alpha.

**`details.ci_high`**

Upper end of the confidence interval for the average difference (Field - Field2).

**`details.effect_size.cohens_d`**

d_z: the average difference divided by the standard deviation of the differences (not the d_av of the two fields' own spreads).

**Sign:**

- `+`: Field is larger than Field2 on average
- `-`: Field2 is larger than Field on average

**Caveats:**

- No sourced bands apply to d_z: Cohen's 0.2 / 0.5 / 0.8 were set for gaps between independent groups. d_z = d / sqrt(2(1 - r)), so it reads larger than a between-group d when Field and Field2 correlate above 0.5 and smaller below it.
- Do not compare d_z directly with a two-group d; report the mean difference alongside it.
- When the standard deviation is zero, d is undefined and the key is left out rather than reported as 0.

More on reading it: [TEST_PAIRED_T](../reading/test.md#op-test_paired_t).

## Pitfalls

<!-- docgen:preserve begin guide-change_over_time-pitfalls -->
- **Before and after is not cause and effect.** Anything else that changed in the same window — season, prices, a gradual drift — lands in the difference too; a comparison group that did not get the change is what isolates it.
- **Periods of different length are not comparable.** February has fewer days than March and a partial last month looks like a collapse; compare averages per day, or complete periods only.
- **Seasonality looks like a trend.** A December peak is not growth; compare with the same period a year earlier, or remove the seasonal pattern first.
- **Thin periods swing wildly.** With few rows per day, daily figures jump around and a trend test may miss a real drift; bucket into weeks or months, and read a short noisy series as inconclusive rather than flat.
- **Smoothing before testing overstates a trend.** A moving average makes neighbouring points depend on each other, so a trend test on the smoothed series looks surer than the data allow; test the raw period figures.
- **Paired data need a paired test.** The same customers before and after are not two independent groups; a paired test compares each customer with themselves and is far more sensitive.
- **Time zones move rows between days.** A timestamp near midnight belongs to different days in different zones; set the zone the business reports in before bucketing.
<!-- docgen:preserve end guide-change_over_time-pitfalls -->

## Next steps

**Related questions:**

- [Compare groups](compare_groups.md)
- [Describe a measure](describe.md)
- [Benchmark against a reference](benchmark.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`feature-engineering`](../skills/feature-engineering.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`statistical-testing`](../skills/statistical-testing.md), [`window-design`](../skills/window-design.md), [`op-test-paired-t`](../skills/op-test-paired-t.md)

**From the command line:**

- `pulse recommend --intent change_over_time --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
