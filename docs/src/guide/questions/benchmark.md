# Benchmark against a reference

## The question

<!-- docgen:preserve begin guide-benchmark-question -->
You have a figure — an average, a share, a total, a score — and something to hold it against: a target you set, the whole population, another group, last wave or a reference request. You want to know whether your figure sits above or below that reference, by how much, and whether the gap is bigger than the noise in the rows you have.

It sounds like:

- "Is our average order value above the target we set?"
- "Which regions sit above the total on revenue?"
- "How does this wave's mix compare with the last one?"

Decide first what the reference is, because it picks the analysis. A fixed figure you supply takes a one-sample test (a t-test for an average). The total or the population takes an index or a z-score overlay: an index of one hundred means level with the reference, and a z-score says how many standard errors the figure sits above or below it. Another group, another wave or another request takes a delta (the plain difference) or an index overlay against that group, wave or request.
<!-- docgen:preserve end guide-benchmark-question -->

## Pick your analysis

This question fits data shaped like:

- `measure`: 1 or more numeric, categorical or bool fields; `group`: one categorical, bool or date field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_AUC`](../catalog/aggregator.md#op-agg_auc) | How well a score separates a yes/no outcome: the chance that a randomly picked yes row scores higher than a randomly picked no row. | intermediate |
| [`AGG_MAE`](../catalog/aggregator.md#op-agg_mae) | Average size of a prediction's miss in the actual's own units, comparing an actual column with a prediction column row by row. | intermediate |
| [`AGG_MAPE`](../catalog/aggregator.md#op-agg_mape) | Average size of a prediction's miss as a share of the actual value, comparing an actual column with a prediction column row by row. | intermediate |
| [`AGG_RMSE`](../catalog/aggregator.md#op-agg_rmse) | Typical size of a prediction's miss in the actual's own units, with large misses counting more: the root of the average squared miss. | intermediate |

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_PERCENTILE`](../catalog/attribute.md#op-attr_percentile) | Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied. | basic |
| [`ATTR_PROFILE_SIMILARITY`](../catalog/attribute.md#op-attr_profile_similarity) | Adds to every row how closely its related fields match a target profile: an ideal pattern, a benchmark, or its own segment target. | intermediate |
| [`ATTR_TSCORE`](../catalog/attribute.md#op-attr_tscore) | Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10. | intermediate |
| [`ATTR_VEC_DISTANCE`](../catalog/attribute.md#op-attr_vec_distance) | Adds to every row how far its set of related fields lies from a target profile, optionally in units of each field's spread. | intermediate |
| [`ATTR_ZSCORE`](../catalog/attribute.md#op-attr_zscore) | Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean. | intermediate |
| [`ATTR_REG_FITTED`](../catalog/attribute.md#op-attr_reg_fitted) | Adds to every row the value a straight-line model predicts for its target from its predictors. | advanced |
| [`ATTR_REG_PREDICT`](../catalog/attribute.md#op-attr_reg_predict) | Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values. | advanced |
| [`ATTR_REG_RESIDUAL`](../catalog/attribute.md#op-attr_reg_residual) | Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it. | advanced |

### Window operators

| Use | In plain words | Level |
|---|---|---|
| [`WIN_DELTA`](../catalog/window.md#op-win_delta) | Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month. | basic |
| [`WIN_DENSE_RANK`](../catalog/window.md#op-win_dense_rank) | Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3. | basic |
| [`WIN_PCT_CHANGE`](../catalog/window.md#op-win_pct_change) | Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month. | basic |
| [`WIN_RANK`](../catalog/window.md#op-win_rank) | Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4. | basic |
| [`WIN_SHARE`](../catalog/window.md#op-win_share) | Adds each row's share of its partition's total, or the running share down the ordered rows, such as the share of defaults caught so far. | basic |
| [`WIN_LIFT`](../catalog/window.md#op-win_lift) | Adds how many times its share of one column a row holds of another, such as a decile's share of defaults over its share of loans. | intermediate |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_T`](../catalog/test.md#op-test_t) | Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version). | intermediate |
| [`TEST_HOTELLING_T2`](../catalog/test.md#op-test_hotelling_t2) | Checks whether several numeric fields, taken together as one profile, differ from a target, between two groups, or before vs after. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_DELTA_VS_MARGIN`](../catalog/overlay.md#op-overlay_delta_vs_margin) | Each crosstab cell minus its row, column or grand margin, in the cell's own units: how far a cell sits above or below its margin. | basic |
| [`OVERLAY_DELTA_VS_SIBLING`](../catalog/overlay.md#op-overlay_delta_vs_sibling) | Each group's value minus one named group's value, in the value's own units: how far every group sits from, say, the control group. | basic |
| [`OVERLAY_INDEX_VS_MARGIN`](../catalog/overlay.md#op-overlay_index_vs_margin) | Index value of each crosstab cell against its row, column or grand margin (cell / margin x 100): which cells over- or under-index? | basic |
| [`OVERLAY_INDEX_VS_POP`](../catalog/overlay.md#op-overlay_index_vs_pop) | Each category's (or histogram bin's) share in a facet subset as an index value against its share in a comparison population (x 100). | basic |
| [`OVERLAY_INDEX_VS_SIBLING`](../catalog/overlay.md#op-overlay_index_vs_sibling) | Each group's value as an index value against one named group (group / sibling x 100), such as every store against the flagship. | basic |
| [`OVERLAY_CHISQ_VS_POP`](../catalog/overlay.md#op-overlay_chisq_vs_pop) | Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix. | intermediate |
| [`OVERLAY_DELTA_VS_REF`](../catalog/overlay.md#op-overlay_delta_vs_ref) | Each cell or group of a target Compose request minus the same coordinate in the reference request, in the target's own units. | intermediate |
| [`OVERLAY_DELTA_VS_STAGE`](../catalog/overlay.md#op-overlay_delta_vs_stage) | A process-chain stage's result minus an earlier stage's result at the same coordinate, in the later stage's own units. | intermediate |
| [`OVERLAY_INDEX_VS_REF`](../catalog/overlay.md#op-overlay_index_vs_ref) | Index value of each cell or group of a target Compose request against the same spot in the reference request (target / reference x 100). | intermediate |
| [`OVERLAY_INDEX_VS_STAGE`](../catalog/overlay.md#op-overlay_index_vs_stage) | Index value of a process-chain stage's result against an earlier stage's result at the same coordinate (target / reference x 100). | intermediate |
| [`OVERLAY_PANEL_INDEX_VS_REF`](../catalog/overlay.md#op-overlay_panel_index_vs_ref) | Index values of several target Compose requests against one shared reference request, one layer per target (target / reference x 100). | intermediate |
| [`OVERLAY_ZSCORE_VS_MARGIN`](../catalog/overlay.md#op-overlay_zscore_vs_margin) | Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out? | intermediate |
| [`OVERLAY_ZSCORE_VS_POP`](../catalog/overlay.md#op-overlay_zscore_vs_pop) | Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset. | intermediate |
| [`OVERLAY_ZSCORE_VS_TOTAL`](../catalog/overlay.md#op-overlay_zscore_vs_total) | How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out? | intermediate |
| [`OVERLAY_KS_VS_POP`](../catalog/overlay.md#op-overlay_ks_vs_pop) | Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's. | advanced |
| [`OVERLAY_RAKE`](../catalog/overlay.md#op-overlay_rake) | Rake a crosstab to known row and column totals: the fitted table, per-cell adjustment factors and whether the fit converged. | advanced |

## A worked example

**Do customers spend more on average than the target of 100 per customer?**

The request (example `t_test_one_sample`; `pulse examples show t_test_one_sample` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_AVERAGE",
      "field": "revenue",
      "label": "avg_revenue"
    },
    {
      "type": "AGG_COUNT",
      "field": "revenue",
      "label": "n"
    }
  ],
  "cohort": {
    "filename": "experiment.pulse",
    "data_dir": ".data"
  },
  "return": {
    "precision": 4
  },
  "tests": [
    {
      "type": "TEST_T",
      "field": "revenue",
      "alpha": 0.05,
      "label": "revenue_vs_100",
      "params": {
        "mu": 100.0
      }
    }
  ]
}
```

The result:

```json
{
  "data": [
    {
      "avg_revenue": 154.3,
      "n": 400
    }
  ],
  "metadata": {
    "total_rows": 400,
    "filtered_rows": 400,
    "cohort_file": "experiment.pulse"
  },
  "tests": [
    {
      "label": "revenue_vs_100",
      "type": "TEST_T",
      "variant": "one_sample",
      "statistic": 11.56,
      "df": 399,
      "p_value": 7.392e-27,
      "alpha": 0.05,
      "reject_null": true,
      "details": {
        "ci_high": 163.5,
        "ci_low": 145.1,
        "effect_size": {
          "cohens_d": 0.578
        },
        "mean": 154.3,
        "mu": 100,
        "n": 400,
        "variance": 8825
      }
    }
  ],
  "components": {
    "aggregations": [
      {
        "label": "avg_revenue",
        "n": 400,
        "n_null": 0,
        "operator": {
          "sum": 6.172e+04
        }
      },
      {
        "label": "n",
        "n": 400,
        "n_null": 0
      }
    ],
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

Yes, comfortably. The average customer brought in about half as much again as the target, and the test says an average this far above the target would almost never turn up if the true average sat at the target: the p-value is tiny and the whole confidence interval lies above it. Cohen's d puts the gap at a medium size next to how much customers vary, so plenty of individual customers can still sit below the target even though the average clears it. The test compares one average with a fixed figure you supply, so choose the target before looking at the data; to benchmark against another group instead, use a two-sample test.

## How to read the result

### `TEST_T`

**`statistic`**

t is the gap between averages measured in standard errors: the sample mean minus the target value (one-sample), or the first group's mean minus the second's (two-sample, Welch). Values far from 0 in either direction are unlikely if the true gap is zero.

**Sign:**

- `+`: the sample mean is above the target, or the first group in details.groups has the higher mean
- `-`: the sample mean is below the target, or the second group in details.groups has the higher mean

**Caveats:**

- The two-sample form is Welch's: degrees of freedom (df) are adjusted for unequal variances and are usually not a whole number.

**`p_value`**

Follows the shared reading: see [Reading a p-value](../reading/test.md#shared-p-value).

**`details.reference`**

Present only with params.reference: the split_by level read second in details.groups, so diff, its interval, the statistic and the signed effect size read the other group minus this one.

**`details.diff`**

Two-sample only: the first group's mean minus the second's, in the field's own units.

**Sign:**

- `+`: the first group in details.groups is higher (alphabetical order; with params.reference, the non-reference group)
- `-`: the second group in details.groups is higher (alphabetical order; with params.reference, the reference)

**`details.ci_low`**

Lower end of the confidence interval at the 1 - alpha level (95% by default): for the mean itself in the one-sample form, for the difference in means (details.diff) in the two-sample form.

**Caveats:**

- In the one-sample form the interval is around the mean, not around mean minus target: compare it with the target value directly.

**`details.ci_high`**

Upper end of the same confidence interval: for the mean (one-sample) or for the difference in means (two-sample).

**`details.effect_size.cohens_d`**

The gap in standard-deviation units: (mean - target) / SD for one sample, (first mean - second mean) / pooled SD for two groups. 0.5 means the averages sit half a standard deviation apart.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.2 | very small |
| from 0.2 to below 0.5 | small |
| from 0.5 to below 0.8 | medium |
| 0.8 and above | large |

**Sign:**

- `+`: the mean is above the target, or the first group in details.groups is higher
- `-`: the mean is below the target, or the second group in details.groups is higher

**Caveats:**

- This d divides by the pooled standard deviation of the two groups, even though the test itself does not assume equal variances; when spreads differ a lot, d describes neither group exactly.
- When the standard deviation is zero, d is undefined and the key is left out rather than reported as 0.

**`details.diff_unadjusted`**

Present only with params.covariate: the raw first-minus-second difference of means on the same rows; details.diff, its interval, the statistic, p, mean, variance and cohens_d are the covariate-adjusted figures.

**Sign:**

- `+`: the first group in details.groups is higher (alphabetical order; with params.reference, the non-reference group)
- `-`: the second group in details.groups is higher (alphabetical order; with params.reference, the reference)

**Caveats:**

- Cohen's d here divides by the adjusted spread, so it is larger than the raw d: compare it only with other adjusted d's.

**`details.theta`**

Present only with params.covariate: the slope of the outcome on the covariate pooled over both groups; each row is read as outcome - theta x (covariate - covariate_mean).

**Caveats:**

- Null when the covariate is constant: nothing is adjusted then. It is estimated from the same data, which standard CUPED practice ignores in the interval.

**`details.covariate_mean`**

Present only with params.covariate: the covariate's mean over both groups, the point the adjustment centres on.

**`details.variance_reduction`**

Present only with params.covariate: the share of the difference's sampling variance the covariate removed (0.6 = the interval is about 37% narrower than unadjusted).

**Caveats:**

- Null when theta is; below 0 means the covariate made the estimate noisier, so drop it. Use a covariate measured before assignment: one the treatment moved biases diff.

**`details.n_covariate_dropped`**

Present only with params.covariate: rows with an outcome and a group but a null covariate, left out of the test (n counts the rest).

More on reading it: [TEST_T](../reading/test.md#op-test_t).

## Pitfalls

<!-- docgen:preserve begin guide-benchmark-pitfalls -->
- **Choose the reference before you look.** A target picked after seeing the data, or the one comparison that happened to look good, makes any gap look convincing; write the reference down first.
- **An index is a ratio, not a test.** An index of one hundred and twenty says the figure is a fifth above the reference; it says nothing about whether that gap could come from the rows you happened to collect. Read the base counts, or use a test or a z-score overlay to judge it.
- **A group is part of its own total.** When a large group is compared with a total it makes up much of, the gap shrinks towards zero; for a fair contrast compare it with everyone else, or with a sibling group.
- **Sums and averages index differently.** Indexing each group's total against the grand total gives its share of the whole, which grows with group size; index an average or a rate when you want to compare how the typical row performs.
- **A small p-value is not a big gap.** With many rows a gap too small to matter still clears the bar; read the size of the gap and the effect size beside the p-value.
- **Many groups against one reference inflate false alarms.** Scoring every region or every cell against the total makes some stand out by luck; use the request's `multiplicity` block to adjust the p-values.
- **Survey data may need a weight.** An unweighted figure describes the sample; set the request's weight when you benchmark against a population figure.
<!-- docgen:preserve end guide-benchmark-pitfalls -->

## Next steps

**Related questions:**

- [Find drivers](drivers.md)
- [Mix and share](composition.md)
- [Compare groups](compare_groups.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`attribute-composition`](../skills/attribute-composition.md), [`facet-design`](../skills/facet-design.md), [`overlay-formula-namespace`](../skills/overlay-formula-namespace.md), [`overlay-pairwise`](../skills/overlay-pairwise.md), [`overlay-reference-tests`](../skills/overlay-reference-tests.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`panel-n-sources`](../skills/panel-n-sources.md), [`process-chain`](../skills/process-chain.md), [`statistical-testing`](../skills/statistical-testing.md), [`test-variant-options`](../skills/test-variant-options.md), [`window-design`](../skills/window-design.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-agg-count`](../skills/op-agg-count.md), [`op-test-t`](../skills/op-test-t.md)

**From the command line:**

- `pulse recommend --intent benchmark --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
