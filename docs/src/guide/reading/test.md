# Reading test results

How to read each output field of the statistical tests this instance offers. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

<a id="shared-p-value"></a>

## Reading a p-value

Every field below marked as following this reading is read the same way.

The chance of seeing a result at least this extreme if the test's null hypothesis were true (usually no difference or link; for a shape test, the stated shape) and its assumptions held. Below the chosen alpha (0.05 unless the request sets another) the result is called significant.

**Caveats:**

- Significant is not the same as important: with enough rows a trivial difference is significant, so read the effect size for how big it is.
- Not significant does not mean there is no difference: the data may simply be too few or too noisy to detect one.
- When more than one test runs, some will be significant by chance alone; adjust for multiple comparisons (set multiplicity, then read p_adjusted beside the raw p-value) or treat isolated hits with caution.
- Under a row weight it rests on the effective sample size reported beside n (sum_weights for frequency weights, Kish's n_eff for probability weights), not on the row count; it accounts for unequal weights only, not strata or clusters, so a design-based p-value from survey software can be larger.

## Statistical tests

<a id="op-test_anova_f"></a>

### `TEST_ANOVA_F`

Checks whether the average of a numeric measure differs across three or more groups. See its [catalog entry](../catalog/test.md#op-test_anova_f).

#### `statistic`

F compares how far apart the group averages are with how much rows vary inside each group; larger values are stronger evidence that the groups differ by more than within-group noise; the p-value says whether this F is large enough to be surprising.

**Caveats:**

- F says whether some groups differ, not which ones: run TEST_TUKEY_HSD to find the pairs.
- The classic F assumes equal variances across groups; with unequal spreads and unequal group sizes prefer TEST_ANOVA_WELCH.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.effect_size.eta_squared`

The share of all variation in the measure that group membership accounts for in this sample, from 0 to 1.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.01 | very small |
| from 0.01 to below 0.06 | small |
| from 0.06 to below 0.14 | medium |
| 0.14 and above | large |

**Caveats:**

- Eta squared overstates the effect in small samples; prefer omega squared when reporting.

#### `details.effect_size.omega_squared`

A less biased estimate of the share of variation group membership accounts for, adjusted for sample size and the number of groups.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.01 | very small |
| from 0.01 to below 0.06 | small |
| from 0.06 to below 0.14 | medium |
| 0.14 and above | large |

**Caveats:**

- The raw estimate goes below zero when F is under 1; Pulse reports 0 instead: the sample shows no measurable share of variation, which does not show that the true effect is zero, especially with small groups.

<a id="op-test_anova_rm"></a>

### `TEST_ANOVA_RM`

Checks whether the average differs across conditions when every subject is measured under each condition. See its [catalog entry](../catalog/test.md#op-test_anova_rm).

#### `statistic`

F compares how far apart the condition averages are with the leftover noise once each subject's own level is removed; larger values are stronger evidence that conditions differ by more than that noise; the p-value says whether this F is large enough to be surprising.

**Caveats:**

- No sphericity correction (such as Greenhouse-Geisser) is applied: with three or more conditions whose differences vary unevenly, the p-value runs too small.
- It says whether some conditions differ, not which ones.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.effect_size.partial_eta_squared`

The share of the within-subject variation, after removing differences between subjects, that the conditions account for, from 0 to 1.

**Caveats:**

- There are no sourced bands for repeated-measures partial eta squared, so none are attached: Cohen's 0.01 / 0.06 / 0.14 were set for between-groups designs, and excluding subject variance makes this figure read larger for the same shift; compare it only with other repeated-measures results.

#### `details.dropped_subjects`

How many subjects were left out because they lacked a value for at least one condition; only complete subjects enter the test.

**Caveats:**

- If dropped subjects differ from the rest (for example, people who quit early), the result describes the completers only.

<a id="op-test_anova_welch"></a>

### `TEST_ANOVA_WELCH`

Checks whether the average of a numeric measure differs across groups when the groups may have unequal spread. See its [catalog entry](../catalog/test.md#op-test_anova_welch).

#### `statistic`

Welch's F compares the spread of the group averages with the noise inside groups, weighting each group by its own variance; larger values are stronger evidence that the averages differ by more than that noise; the p-value says whether this F is large enough to be surprising.

**Caveats:**

- It says whether some groups differ, not which ones.
- The df slot holds the between-groups degrees of freedom; the adjusted within-groups df is in details.df_within and is usually not a whole number.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.effect_size.omega_squared`

Estimated share of variation group membership accounts for, from Welch's F plugged into the classic omega-squared formula; it approximates TEST_ANOVA_F's omega squared and matches it exactly only when the variances are equal.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.01 | very small |
| from 0.01 to below 0.06 | small |
| from 0.06 to below 0.14 | medium |
| 0.14 and above | large |

**Caveats:**

- The raw estimate goes below zero when F is under 1; Pulse reports 0 instead: the sample shows no measurable share of variation, which does not show that the true effect is zero, especially with small groups.

<a id="op-test_brown_forsythe"></a>

### `TEST_BROWN_FORSYTHE`

Checks whether the spread of a numeric field differs across groups: a robust test of equal variances. See its [catalog entry](../catalog/test.md#op-test_brown_forsythe).

#### `statistic`

F on each row's distance from its group median: larger values are stronger evidence that the groups differ in spread; the p-value says whether this F is large enough to be surprising.

**Caveats:**

- It tests spread only, not averages.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

<a id="op-test_chisq"></a>

### `TEST_CHISQ`

Checks whether two categorical fields are associated by comparing a cross-tabulation with the counts expected if unrelated. See its [catalog entry](../catalog/test.md#op-test_chisq).

#### `statistic`

Pearson's chi-square adds up, over every cell, how far the observed count is from the count expected if the two fields were unrelated. Larger values mean the table departs more from that pattern.

**Caveats:**

- Chi-square grows with the number of rows, so it does not measure the strength of the association: read Cramer's V.
- No continuity (Yates) correction is applied, so on a 2x2 table the p-value is smaller than R's chisq.test default.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.expected_min`

The smallest expected count across all cells.

**Caveats:**

- Below 5 the chi-square approximation becomes unreliable (Pulse also warns); for a 2x2 table use TEST_FISHER_EXACT instead.

#### `details.effect_size.cramers_v`

Cramer's V, the strength of the association from 0 (unrelated) to 1 (one field fully determines the other), comparable across sample sizes.

**Caveats:**

- There are no standard bands that hold for every table size: Cohen's small / medium / large benchmarks for V are 0.1 / 0.3 / 0.5 divided by the square root of df*, the smaller of rows - 1 and columns - 1.
- V carries no direction; inspect the table to see which cells drive it.

#### `details.effect_size.phi`

Phi, reported for 2x2 tables only: the strength of the association from 0 to 1, equal to Cramer's V and to Cohen's w there.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.1 | very small |
| from 0.1 to below 0.3 | small |
| from 0.3 to below 0.5 | medium |
| 0.5 and above | large |

**Caveats:**

- Pulse reports phi unsigned; inspect the table to see which way the association runs.

<a id="op-test_fisher_exact"></a>

### `TEST_FISHER_EXACT`

Exact test of whether two yes/no fields are associated, built for small 2x2 tables. See its [catalog entry](../catalog/test.md#op-test_fisher_exact).

#### `statistic`

The sample odds ratio (a*d)/(b*c) of the 2x2 table in details.contingency, also in details.odds_ratio: 1 means the odds of the first column are the same in both rows, above 1 higher in the first row, below 1 lower.

**Bands** (convention: Cohen (1988) d benchmarks converted via d = ln(OR)*sqrt(3)/pi (Chinn 2000); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 1.44 | very small |
| from 1.44 to below 2.48 | small |
| from 2.48 to below 4.27 | medium |
| 4.27 and above | large |

**Caveats:**

- Bands apply to max(OR, 1/OR), so an odds ratio of 0.25 reads like one of 4.
- Rows and columns follow the order values were first seen (details.row_labels / col_labels), so the ratio can come out inverted from what you expect: check the labels.
- A zero in b or c gives an infinite ratio and a zero in a or d gives 0; with zero cells read the p-value and the table, not the ratio.
- This is the plain cross-product ratio, not the conditional estimate R's fisher.test reports, so the two differ slightly.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

<a id="op-test_kendall_tau"></a>

### `TEST_KENDALL_TAU`

Measures how often pairs of rows agree in order on two numeric fields: a rank-based link suited to small samples and ties. See its [catalog entry](../catalog/test.md#op-test_kendall_tau).

#### `statistic`

Kendall's tau-b, from -1 to +1: the share of row pairs that are ordered the same way on both fields minus the share ordered the opposite way, adjusted for ties.

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- There are no sourced bands for tau, so none are attached; tau usually comes out smaller than Pearson's r or Spearman's rho for the same data, so do not read it on their scale.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

<a id="op-test_kruskal_wallis"></a>

### `TEST_KRUSKAL_WALLIS`

Rank-based check of whether values tend to be larger in some groups than in others, across two or more groups. See its [catalog entry](../catalog/test.md#op-test_kruskal_wallis).

#### `statistic`

H measures how far each group's average rank sits from the overall average rank, corrected for ties; larger values are stronger evidence that some groups tend to have higher values than others; the p-value says whether this H is large enough to be surprising.

**Caveats:**

- It says whether some groups tend to be higher, not which ones: follow up with pairwise rank tests adjusted for multiple comparisons.
- The p-value reads H against a chi-square distribution with groups minus 1 degrees of freedom, which needs roughly five or more rows per group.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.effect_size.epsilon_squared`

The share of variation in the ranks that group membership accounts for, from 0 to 1.

**Caveats:**

- There are no sourced bands for rank-based epsilon squared, so none are attached; Cohen's eta-squared benchmarks were set for raw-value variance.
- It is computed on ranks, so it describes how well groups order the values, not the share of variation in the raw values.
- Left out when every value is tied.

<a id="op-test_ks"></a>

### `TEST_KS`

Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape. See its [catalog entry](../catalog/test.md#op-test_ks).

#### `statistic`

D is the largest vertical gap between the two groups' cumulative distributions, from 0 (identical) to 1 (no overlap at all).

**Caveats:**

- D is most sensitive to differences near the middle of the distributions and less to differences in the tails.
- D shows that the shapes differ, not how: plot both distributions to see whether it is the centre, the spread or the tails.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

<a id="op-test_mann_whitney_u"></a>

### `TEST_MANN_WHITNEY_U`

Rank-based check of whether values in one of two groups tend to be larger than in the other. See its [catalog entry](../catalog/test.md#op-test_mann_whitney_u).

#### `statistic`

U counts, over every pair of rows from the two groups, how often one group's value is larger (ties count half). Pulse reports the smaller of the two counts, from 0 to half of n_A times n_B: the smaller U, the more the groups separate.

**Caveats:**

- U carries no direction; read details.z or the rank-biserial for which group tends to be larger.
- R's wilcox.test reports the first group's count instead of the smaller one, so the two can disagree while the p-values match.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.z`

The normal score the p-value is read from, with a continuity correction of one half.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

**Caveats:**

- The p-value always uses this large-sample approximation, even for small groups where an exact test would be more accurate.

#### `details.effect_size.rank_biserial`

Rank-biserial correlation, from -1 to +1: the chance that a random row from the first group beats one from the second, minus the reverse.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

**Caveats:**

- There are no sourced bands for rank-biserial r that Pulse could verify, so none are attached; (r + 1) / 2 is the probability that a first-group row is larger, ties counting half.
- It measures how often one group's values exceed the other's, not a difference in medians.

<a id="op-test_paired_t"></a>

### `TEST_PAIRED_T`

Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero. See its [catalog entry](../catalog/test.md#op-test_paired_t).

#### `statistic`

t is the average of the per-row differences (Field - Field2) measured in standard errors of that average. Values far from 0 in either direction are unlikely if the true average difference is zero.

**Sign:**

- `+`: Field is larger than Field2 on average
- `-`: Field2 is larger than Field on average

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.mean_diff`

The average of Field - Field2 over complete pairs, in the field's own units.

**Sign:**

- `+`: Field is larger than Field2 on average
- `-`: Field2 is larger than Field on average

#### `details.ci_low`

Lower end of the confidence interval for the average difference (Field - Field2) at the 1 - alpha level (95% by default).

**Caveats:**

- An interval that excludes 0 matches a p-value below alpha.

#### `details.ci_high`

Upper end of the confidence interval for the average difference (Field - Field2).

#### `details.effect_size.cohens_d`

d_z: the average difference divided by the standard deviation of the differences (not the d_av of the two fields' own spreads).

**Sign:**

- `+`: Field is larger than Field2 on average
- `-`: Field2 is larger than Field on average

**Caveats:**

- No sourced bands apply to d_z: Cohen's 0.2 / 0.5 / 0.8 were set for gaps between independent groups. d_z = d / sqrt(2(1 - r)), so it reads larger than a between-group d when Field and Field2 correlate above 0.5 and smaller below it.
- Do not compare d_z directly with a two-group d; report the mean difference alongside it.
- When the standard deviation is zero, d is undefined and the key is left out rather than reported as 0.

<a id="op-test_pearson_r"></a>

### `TEST_PEARSON_R`

Measures how strongly two numeric fields rise and fall together along a straight line. See its [catalog entry](../catalog/test.md#op-test_pearson_r).

#### `statistic`

r measures how closely the two fields follow a straight line together, from -1 to +1; 0 means no straight-line link.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.1 | very small |
| from 0.1 to below 0.3 | small |
| from 0.3 to below 0.5 | medium |
| 0.5 and above | large |

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- An r near zero rules out only a straight-line link; a curved relationship can still be strong.
- A few extreme rows, or a narrow range of either field, can move r a lot.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.ci_low`

Lower end of the confidence interval for r at the 1 - alpha level (95% by default).

**Caveats:**

- With fewer than four pairs, or a perfect r, no interval can be computed and both ends are set to r: this is not a precise estimate. Treat r as highly uncertain and do not report the interval.

#### `details.ci_high`

Upper end of the confidence interval for r at the 1 - alpha level (95% by default).

<a id="op-test_prop_z"></a>

### `TEST_PROP_Z`

Checks whether the rate of one outcome, such as conversion, differs between two groups. See its [catalog entry](../catalog/test.md#op-test_prop_z).

#### `statistic`

z: the first group's success rate minus the second's, in standard errors computed from the pooled rate. Values far from 0 in either direction are unlikely if the true rates are equal.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

**Caveats:**

- The normal approximation needs at least 10 successes and 10 failures in each group.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.diff`

The first group's success rate minus the second's, as a proportion (0.05 = 5 percentage points).

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

#### `details.ci_low`

Lower end of the confidence interval for details.diff at the 1 - alpha level (95% by default).

**Caveats:**

- The interval uses the unpooled (Wald) standard error while the test uses the pooled one, so near the alpha boundary the two can disagree.
- The Wald interval is poor with few successes or failures, or rates near 0 or 1.

#### `details.ci_high`

Upper end of the confidence interval for details.diff.

#### `details.effect_size.cohens_h`

Cohen's h, the gap between the two rates on an arcsine scale that treats a change from 0.01 to 0.05 as larger than one from 0.45 to 0.49.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.2 | very small |
| from 0.2 to below 0.5 | small |
| from 0.5 to below 0.8 | medium |
| 0.8 and above | large |

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

**Caveats:**

- Cohen's 0.2 / 0.5 / 0.8 benchmarks for h are the same as for d, applied to |h|.

<a id="op-test_shapiro_wilk"></a>

### `TEST_SHAPIRO_WILK`

Checks whether a numeric field looks normally distributed, overall or within each group. See its [catalog entry](../catalog/test.md#op-test_shapiro_wilk).

#### `statistic`

W' (Shapiro-Francia) measures how closely the sorted values follow the straight line expected of normal data, up to 1; values clearly below 1 point to skew, heavy tails or other departures from normal.

**Caveats:**

- With SplitBy, the headline W' and p-value belong to the group with the smallest p; read details.per_group for every group.

#### `p_value`

**Caveats:**

- A small p-value is evidence against normality; a large one only means a departure was not detected, which is likely with few rows.
- With SplitBy the headline is the smallest p across groups, so it is more likely to be small than any single group's p.

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.per_group`

One entry per group (or one overall without SplitBy) with n, w, z and p_value, plus a warning when the p-value is advisory (fewer than 5 or more than 5000 rows, or a degenerate sample).

<a id="op-test_spearman_r"></a>

### `TEST_SPEARMAN_R`

Measures how consistently one numeric field rises (or falls) as the other rises, using ranks, so the link need not be a straight line. See its [catalog entry](../catalog/test.md#op-test_spearman_r).

#### `statistic`

Spearman's rho is Pearson's r computed on ranks, from -1 to +1: how consistently one field rises (or falls) as the other rises, whether or not in a straight line.

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- There are no sourced bands for rho, so none are attached: Cohen's r benchmarks were set for Pearson's r.
- The p-value uses a t approximation with n - 2 degrees of freedom; many ties make it unreliable (Pulse warns).

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

<a id="op-test_t"></a>

### `TEST_T`

Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version). See its [catalog entry](../catalog/test.md#op-test_t).

#### `statistic`

t is the gap between averages measured in standard errors: the sample mean minus the target value (one-sample), or the first group's mean minus the second's (two-sample, Welch). Values far from 0 in either direction are unlikely if the true gap is zero.

**Sign:**

- `+`: the sample mean is above the target, or the first group in details.groups has the higher mean
- `-`: the sample mean is below the target, or the second group in details.groups has the higher mean

**Caveats:**

- The two-sample form is Welch's: degrees of freedom (df) are adjusted for unequal variances and are usually not a whole number.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.diff`

Two-sample only: the first group's mean minus the second's, in the field's own units.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

#### `details.ci_low`

Lower end of the confidence interval at the 1 - alpha level (95% by default): for the mean itself in the one-sample form, for the difference in means (details.diff) in the two-sample form.

**Caveats:**

- In the one-sample form the interval is around the mean, not around mean minus target: compare it with the target value directly.

#### `details.ci_high`

Upper end of the same confidence interval: for the mean (one-sample) or for the difference in means (two-sample).

#### `details.effect_size.cohens_d`

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

<a id="op-test_trend"></a>

### `TEST_TREND`

Checks whether an ordered series, such as monthly totals, tends to keep rising or keep falling. See its [catalog entry](../catalog/test.md#op-test_trend).

#### `statistic`

The Mann-Kendall Z score: the trend count S (details.s) in standard errors, with a continuity correction. Values far from 0 point to a consistent tendency to rise or fall (not necessarily at an even rate).

**Sign:**

- `+`: the series tends to rise over the OrderBy sequence
- `-`: the series tends to fall over the OrderBy sequence

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.s`

S counts every pair of points: +1 when the later point is higher, -1 when it is lower, 0 when tied.

**Sign:**

- `+`: more later points are higher than earlier ones
- `-`: more later points are lower than earlier ones

#### `details.tau`

S as a share of all pairs of points, from -1 (always falling) to +1 (always rising): how consistent the trend is, not how steep.

**Sign:**

- `+`: a rising trend
- `-`: a falling trend

**Caveats:**

- Tied points count as neither rising nor falling, so many ties pull tau toward 0.

<a id="op-test_tukey_hsd"></a>

### `TEST_TUKEY_HSD`

After an ANOVA, compares every pair of group averages to find which ones differ, keeping the overall false-alarm rate in check. See its [catalog entry](../catalog/test.md#op-test_tukey_hsd).

#### `statistic`

Not used: TEST_TUKEY_HSD reports no single statistic and leaves this slot at 0. Each pair's studentized range q is in details.comparisons.

#### `p_value`

**Caveats:**

- This is the smallest Tukey-adjusted p-value across all pairs, already corrected for multiple comparisons; read details.comparisons for which pairs it belongs to.

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.comparisons`

One entry per pair of groups (a, b in alphabetical order): diff is mean a minus mean b, q the studentized range, p_adj the family-wise adjusted p-value, and ci_low / ci_high a simultaneous confidence interval for diff.

**Caveats:**

- p_adj and the intervals already allow for the many comparisons made, so do not adjust them again.
- An interval that excludes 0 matches p_adj below alpha.

<a id="op-test_welch"></a>

### `TEST_WELCH`

Checks whether the average of a numeric field differs between two groups, without assuming they have equal spread. See its [catalog entry](../catalog/test.md#op-test_welch).

#### `statistic`

Welch's t: the first group's mean minus the second's, in standard errors computed from each group's own variance. Values far from 0 in either direction are unlikely if the true gap is zero.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

**Caveats:**

- Degrees of freedom (df) come from the Welch-Satterthwaite formula and are usually not a whole number.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.diff`

The first group's mean minus the second's, in the field's own units.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

#### `details.ci_low`

Lower end of the Interval for the difference between the two group averages (first minus second) at the 1 - alpha level (95% by default).

**Caveats:**

- An interval that excludes 0 matches a p-value below alpha; its width shows how precisely the difference is pinned down.

#### `details.ci_high`

Upper end of the Interval for the difference between the two group averages (first minus second) at the 1 - alpha level (95% by default).

#### `details.effect_size.cohens_d`

The gap between the two means in pooled-standard-deviation units; 0.5 means they sit half a standard deviation apart.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.2 | very small |
| from 0.2 to below 0.5 | small |
| from 0.5 to below 0.8 | medium |
| 0.8 and above | large |

**Sign:**

- `+`: the first group in details.groups has the higher average
- `-`: the second group in details.groups has the higher average

**Caveats:**

- This d divides by the pooled standard deviation of the two groups, even though the test itself does not assume equal variances; when spreads differ a lot, d describes neither group exactly.
- When the standard deviation is zero, d is undefined and the key is left out rather than reported as 0.

<a id="op-test_wilcoxon_sr"></a>

### `TEST_WILCOXON_SR`

Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve. See its [catalog entry](../catalog/test.md#op-test_wilcoxon_sr).

#### `statistic`

The smaller of the two signed-rank sums: rank the non-zero differences (Field - Field2) by size, add the ranks of the positive ones and of the negative ones, and keep the smaller. The smaller it is, the more one direction dominates.

**Caveats:**

- It carries no direction; read details.z or the rank-biserial for which field tends to be larger.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.z`

The normal score the p-value is read from, with a continuity correction of one half.

**Sign:**

- `+`: Field tends to be larger than Field2
- `-`: Field2 tends to be larger than Field

**Caveats:**

- The p-value always uses this large-sample approximation, even for few pairs where an exact test would be more accurate.

#### `details.zero_diffs`

How many pairs had Field equal to Field2; they are dropped before ranking, and details.n counts only the pairs that remain.

**Caveats:**

- Many zero differences shrink the test to the pairs that changed; report how many were set aside.

#### `details.effect_size.rank_biserial`

Matched-pairs rank-biserial correlation, from -1 to +1: the rank mass of positive differences minus that of negative ones, as a share of the total.

**Sign:**

- `+`: Field tends to be larger than Field2
- `-`: Field2 tends to be larger than Field

**Caveats:**

- There are no sourced bands for rank-biserial r that Pulse could verify, so none are attached.
- Computed over non-zero differences only, matching the test.

<a id="op-test_z_two_sample"></a>

### `TEST_Z_TWO_SAMPLE`

Large-sample check of whether two groups differ in average, with the p-value read from the normal distribution. See its [catalog entry](../catalog/test.md#op-test_z_two_sample).

#### `statistic`

z: the first group's mean minus the second's, in standard errors built from each group's sample variance, read against the normal distribution. Values far from 0 in either direction are unlikely if the true gap is zero.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

**Caveats:**

- The normal reference is only accurate for large groups; with small groups the p-value is too small, so prefer TEST_WELCH.

#### `p_value`

Follows the shared reading: see [Reading a p-value](#shared-p-value).

#### `details.diff`

The first group's mean minus the second's, in the field's own units.

**Sign:**

- `+`: the first group in details.groups (alphabetical order) is higher
- `-`: the second group in details.groups (alphabetical order) is higher

#### `details.ci_low`

Lower end of the normal-theory Interval for the difference between the two group averages (first minus second) at the 1 - alpha level (95% by default).

**Caveats:**

- An interval that excludes 0 matches a p-value below alpha; its width shows how precisely the difference is pinned down.

#### `details.ci_high`

Upper end of the normal-theory Interval for the difference between the two group averages (first minus second) at the 1 - alpha level (95% by default).

#### `details.effect_size.cohens_d`

The gap between the two means in pooled-standard-deviation units; 0.5 means they sit half a standard deviation apart.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.2 | very small |
| from 0.2 to below 0.5 | small |
| from 0.5 to below 0.8 | medium |
| 0.8 and above | large |

**Sign:**

- `+`: the first group in details.groups has the higher average
- `-`: the second group in details.groups has the higher average

**Caveats:**

- This d divides by the pooled standard deviation of the two groups, even though the test itself does not assume equal variances; when spreads differ a lot, d describes neither group exactly.
- When the standard deviation is zero, d is undefined and the key is left out rather than reported as 0.
