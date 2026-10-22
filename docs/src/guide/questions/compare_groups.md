# Compare groups

## The question

<!-- docgen:preserve begin guide-compare_groups-question -->
You have two or more groups — treatment and control, regions, customer segments — and one outcome, such as revenue, a score or whether someone converted. You want to know whether the groups really differ on that outcome, by how much, and whether the gap is bigger than the noise you would expect from the rows you happened to collect.

It sounds like:

- "Did the variant arm spend more than control?"
- "Do the four regions convert at different rates?"
- "Is this segment's satisfaction score different from everyone else's?"

Start with the group figures themselves (an average or a share per group), then add a test that says how surprising the gap would be if the groups were alike. Two groups take a two-sample test; three or more take an analysis of variance or its rank-based cousin, followed by pairwise comparisons only if you need to know which pair differs.
<!-- docgen:preserve end guide-compare_groups-question -->

## Pick your analysis

This question fits data shaped like:

- `outcome`: one numeric, categorical or bool field; `group`: one categorical or bool field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Aggregators

| Use | In plain words | Level |
|---|---|---|
| [`AGG_CI_LOWER`](../catalog/aggregator.md#op-agg_ci_lower) | Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. | intermediate |
| [`AGG_CI_UPPER`](../catalog/aggregator.md#op-agg_ci_upper) | Upper end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. | intermediate |
| [`AGG_VEC_MEAN`](../catalog/aggregator.md#op-agg_vec_mean) | Average of each item of a battery or mix, as one list per group: the group's average profile across the items. | intermediate |
| [`AGG_WELFORD`](../catalog/aggregator.md#op-agg_welford) | Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs. | intermediate |

### Groupers

| Use | In plain words | Level |
|---|---|---|
| [`GROUP_CATEGORY`](../catalog/grouper.md#op-group_category) | Splits the rows into one group per distinct value of a field, such as one group per region or per answer option. | basic |
| [`GROUP_DATE_RANGES`](../catalog/grouper.md#op-group_date_ranges) | Labels each row with the named date range its date falls in, such as custom fiscal quarters or before and after a launch. | basic |
| [`GROUP_NUMERIC_RANGES`](../catalog/grouper.md#op-group_numeric_ranges) | Labels each row with the named number range its value falls in, such as a train/holdout split on a row index or custom score bands. | intermediate |
| [`GROUP_SET_PER_ELEMENT`](../catalog/grouper.md#op-group_set_per_element) | Splits a multi-select answer into one group per option, counting each row once in every option it picked. | intermediate |

### Statistical tests

| Use | In plain words | Level |
|---|---|---|
| [`TEST_PROP_Z`](../catalog/test.md#op-test_prop_z) | Checks whether the rate of one outcome, such as conversion, differs between two groups, or from a planned rate. | basic |
| [`TEST_ANOVA_F`](../catalog/test.md#op-test_anova_f) | Checks whether the average of a numeric measure differs across three or more groups. | intermediate |
| [`TEST_ANOVA_WELCH`](../catalog/test.md#op-test_anova_welch) | Checks whether the average of a numeric measure differs across groups when the groups may have unequal spread. | intermediate |
| [`TEST_CHISQ`](../catalog/test.md#op-test_chisq) | Checks whether two categorical fields are associated, or whether one categorical field's counts match planned proportions. | intermediate |
| [`TEST_FISHER_EXACT`](../catalog/test.md#op-test_fisher_exact) | Exact test of whether two yes/no fields are associated, built for small 2x2 tables. | intermediate |
| [`TEST_KRUSKAL_WALLIS`](../catalog/test.md#op-test_kruskal_wallis) | Rank-based check of whether values tend to be larger in some groups than in others, across two or more groups. | intermediate |
| [`TEST_KS`](../catalog/test.md#op-test_ks) | Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape. | intermediate |
| [`TEST_MANN_WHITNEY_U`](../catalog/test.md#op-test_mann_whitney_u) | Rank-based check of whether values in one of two groups tend to be larger than in the other. | intermediate |
| [`TEST_PAIRED_T`](../catalog/test.md#op-test_paired_t) | Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero. | intermediate |
| [`TEST_T`](../catalog/test.md#op-test_t) | Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version). | intermediate |
| [`TEST_WELCH`](../catalog/test.md#op-test_welch) | Checks whether the average of a numeric field differs between two groups, without assuming they have equal spread. | intermediate |
| [`TEST_WILCOXON_SR`](../catalog/test.md#op-test_wilcoxon_sr) | Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve. | intermediate |
| [`TEST_Z_TWO_SAMPLE`](../catalog/test.md#op-test_z_two_sample) | Large-sample check of whether two groups differ in average, with the p-value read from the normal distribution. | intermediate |
| [`TEST_ANOVA_RM`](../catalog/test.md#op-test_anova_rm) | Checks whether the average differs across conditions when every subject is measured under each condition. | advanced |
| [`TEST_BOX_M`](../catalog/test.md#op-test_box_m) | Checks whether several groups share one spread pattern across a set of numeric fields, the assumption behind comparing average profiles. | advanced |
| [`TEST_BROWN_FORSYTHE`](../catalog/test.md#op-test_brown_forsythe) | Checks whether the spread of a numeric field differs across groups: a robust test of equal variances. | advanced |
| [`TEST_HOTELLING_T2`](../catalog/test.md#op-test_hotelling_t2) | Checks whether several numeric fields, taken together as one profile, differ from a target, between two groups, or before vs after. | advanced |
| [`TEST_MANOVA`](../catalog/test.md#op-test_manova) | Checks whether several numeric fields, taken together as one profile, differ across two or more groups. | advanced |
| [`TEST_TUKEY_HSD`](../catalog/test.md#op-test_tukey_hsd) | After an ANOVA, compares every pair of group averages to find which ones differ, keeping the overall false-alarm rate in check. | advanced |

### Matrix operators

| Use | In plain words | Level |
|---|---|---|
| [`MAT_DISTANCE`](../catalog/matrix.md#op-mat_distance) | How far apart segments are across several numbers at once: the gap between each pair of segment centres. | advanced |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_CHISQ_COL`](../catalog/overlay.md#op-overlay_chisq_col) | Chi-square test per crosstab column: checks whether each column's spread across the rows departs from the table's overall row mix. | intermediate |
| [`OVERLAY_CHISQ_ROW`](../catalog/overlay.md#op-overlay_chisq_row) | Chi-square test per crosstab row: checks whether each row's spread across the columns departs from the table's overall column mix. | intermediate |
| [`OVERLAY_MATRIX_GROUP_SUMMARY`](../catalog/overlay.md#op-overlay_matrix_group_summary) | Summarize a grouped matrix across its groups: each cell's mean, minimum, maximum and range, and how many groups fed it. | intermediate |
| [`OVERLAY_PROP_Z_CELL`](../catalog/overlay.md#op-overlay_prop_z_cell) | Two-proportion z-test for every crosstab cell in a Compose request: does the target's share in that cell differ from the reference's? | intermediate |
| [`OVERLAY_CHISQ_VS_REF`](../catalog/overlay.md#op-overlay_chisq_vs_ref) | Chi-square test in a Compose request: checks whether a target request's crosstab mix departs from the reference request's mix. | advanced |
| [`OVERLAY_FISHER_EXACT_CELL`](../catalog/overlay.md#op-overlay_fisher_exact_cell) | Fisher's exact test for every crosstab cell: checks whether being in that row goes with being in that column more or less than expected. | advanced |
| [`OVERLAY_PAIRWISE_PROBIT_T`](../catalog/overlay.md#op-overlay_pairwise_probit_t) | Pairwise t-tests on probit-transformed shares between rows (or columns) of one crosstab, matching a convention some survey tools use. | advanced |
| [`OVERLAY_PAIRWISE_PROP_Z`](../catalog/overlay.md#op-overlay_pairwise_prop_z) | Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share? | advanced |
| [`OVERLAY_PAIRWISE_TWO_MEANS_Z`](../catalog/overlay.md#op-overlay_pairwise_two_means_z) | Large-sample z-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean? | advanced |
| [`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](../catalog/overlay.md#op-overlay_pairwise_weighted_two_means_z) | Large-sample z-tests between every pair of rows (or columns) of one crosstab of weighted averages, with a sample-size basis you choose. | advanced |
| [`OVERLAY_PAIRWISE_WELCH_T`](../catalog/overlay.md#op-overlay_pairwise_welch_t) | Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean? | advanced |
| [`OVERLAY_PROP_Z_PANEL`](../catalog/overlay.md#op-overlay_prop_z_panel) | Two-proportion z-tests between every pair of requests in a Compose panel, for each crosstab cell: which waves or markets differ in share? | advanced |
| [`OVERLAY_T_CELL`](../catalog/overlay.md#op-overlay_t_cell) | Welch t-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's? | advanced |
| [`OVERLAY_T_VS_REF`](../catalog/overlay.md#op-overlay_t_vs_ref) | Welch t-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's? | advanced |
| [`OVERLAY_Z_CELL`](../catalog/overlay.md#op-overlay_z_cell) | Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's? | advanced |
| [`OVERLAY_Z_VS_REF`](../catalog/overlay.md#op-overlay_z_vs_ref) | Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's? | advanced |

## A worked example

**Do customers in the variant arm spend more than customers in the control arm?**

The request (example `t_test_two_sample`; `pulse examples show t_test_two_sample` prints it):

```json
{
  "aggregations": [
    {
      "type": "AGG_AVERAGE",
      "field": "revenue",
      "label": "avg_revenue"
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
      "split_by": "treatment",
      "alpha": 0.05,
      "label": "revenue_treatment_ttest"
    }
  ]
}
```

The result:

```json
{
  "data": [
    {
      "avg_revenue": 154.3
    }
  ],
  "metadata": {
    "total_rows": 400,
    "filtered_rows": 400,
    "cohort_file": "experiment.pulse"
  },
  "tests": [
    {
      "label": "revenue_treatment_ttest",
      "type": "TEST_T",
      "variant": "welch_two_sample",
      "statistic": -2.72,
      "df": 361.3,
      "p_value": 0.006843,
      "alpha": 0.05,
      "reject_null": true,
      "details": {
        "ci_high": -7.114,
        "ci_low": -44.25,
        "diff": -25.68,
        "effect_size": {
          "cohens_d": -0.2756
        },
        "groups": [
          "control",
          "variant"
        ],
        "mean": [
          142.2,
          167.8
        ],
        "n": [
          211,
          189
        ],
        "variance": [
          6933,
          1.064e+04
        ]
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

Yes. The variant arm's average revenue is higher than the control arm's by close to a fifth, and the test says a gap this wide would rarely appear if the arms truly spent the same: the p-value sits well below alpha (the false-alarm rate the request accepts) and the interval for the difference stays on one side of zero. The difference reads as the first group minus the second (control minus variant), which is why it is negative. Cohen's d puts the effect at the small end, so the two arms overlap heavily and plenty of control customers still out-spend variant customers. The average in the data row is the whole cohort; the per-arm means sit in the test's details.

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

<!-- docgen:preserve begin guide-compare_groups-pitfalls -->
- **A small p-value is not a big difference.** With many rows, a gap too small to matter still clears the bar; read the size of the gap and the effect size (Cohen's d, Cramér's V) beside the p-value.
- **A large p-value is not proof the groups are alike.** With few rows per group a real gap can go undetected; read the interval for the difference, which shows how large a gap the data still allows.
- **Many comparisons inflate false alarms.** Testing every pair of regions, or every cell of a banner, makes some gap clear the bar by luck; use a test across all groups first, a pairwise procedure built for it (Tukey's HSD), or a request's `multiplicity` block to adjust the p-values.
- **Pick the test for the outcome.** Means of a numeric outcome take a t-test or ANOVA; shares of a yes/no outcome take a proportion or chi-square test; a skewed outcome with outliers is safer with a rank-based test (Mann-Whitney, Kruskal-Wallis).
- **Same people measured twice are not two groups.** Before-and-after scores on the same customers need a paired test; treating them as independent groups throws away the pairing and usually hides the change.
- **Groups that differ in make-up confound the comparison.** If one region has more large accounts, its higher average may reflect the mix, not the region; compare like with like or move to the drivers question.
- **Survey data may need a weight.** An unweighted comparison describes the sample; set the request's weight when the rows do not represent the population evenly.
<!-- docgen:preserve end guide-compare_groups-pitfalls -->

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Change over time](change_over_time.md)
- [Relationship between measures](relationship.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`statistical-testing`](../skills/statistical-testing.md), [`op-agg-average`](../skills/op-agg-average.md), [`op-test-t`](../skills/op-test-t.md)

**From the command line:**

- `pulse recommend --intent compare_groups --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
