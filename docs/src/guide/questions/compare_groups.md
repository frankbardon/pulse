# Compare groups

## The question

Questions of this kind sound like:

- Do A and B differ?
- Is this segment different from the rest?

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

No worked example is published for this question yet. `pulse recommend --intent compare_groups` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Related questions:**

- [Describe a measure](describe.md)
- [Change over time](change_over_time.md)
- [Relationship between measures](relationship.md)

**Skills:** [`intents`](../skills/intents.md), [`aggregation-design`](../skills/aggregation-design.md), [`grouper-design`](../skills/grouper-design.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md), [`statistical-testing`](../skills/statistical-testing.md)

**From the command line:**

- `pulse recommend --intent compare_groups --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
