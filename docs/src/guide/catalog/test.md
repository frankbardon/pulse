# Statistical tests

The statistical tests this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`TEST_ANOVA_F`](#op-test_anova_f) | Checks whether the average of a numeric measure differs across three or more groups. | Does average spend differ across regions? | intermediate | [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when the groups have clearly unequal spread.<br>[`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when the measure is heavily skewed or only ordered.<br>[`TEST_ANOVA_RM`](#op-test_anova_rm) when the same subjects are measured in every group.<br>[`TEST_WELCH`](#op-test_welch) when there are only two groups.<br>[`TEST_TUKEY_HSD`](#op-test_tukey_hsd) when you need to know which pairs of groups differ. |
| [`TEST_ANOVA_RM`](#op-test_anova_rm) | Checks whether the average differs across conditions when every subject is measured under each condition. | Do the same panel members rate the three ad concepts differently on average? | advanced | [`TEST_ANOVA_F`](#op-test_anova_f) when each group holds different, unrelated subjects.<br>[`TEST_PAIRED_T`](#op-test_paired_t) when there are only two conditions. |
| [`TEST_ANOVA_WELCH`](#op-test_anova_welch) | Checks whether the average of a numeric measure differs across groups when the groups may have unequal spread. | Does average response time differ across regions whose variability is very different? | intermediate | [`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when the measure is heavily skewed or only ordered.<br>[`TEST_ANOVA_RM`](#op-test_anova_rm) when the same subjects are measured in every group.<br>[`TEST_WELCH`](#op-test_welch) when there are only two groups, or you want pairwise follow-ups: run TEST_WELCH per pair and set multiplicity on the request (for example holm) to adjust their p-values for multiple comparisons.<br>[`TEST_ANOVA_F`](#op-test_anova_f) when spreads are similar and you want Tukey's pairwise follow-up. |
| [`TEST_BROWN_FORSYTHE`](#op-test_brown_forsythe) | Checks whether the spread of a numeric field differs across groups: a robust test of equal variances. | Is delivery time more variable at some warehouses than at others? | advanced | [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when you want to compare the group averages themselves.<br>[`TEST_SHAPIRO_WILK`](#op-test_shapiro_wilk) when you want to check whether a measure is bell-shaped. |
| [`TEST_CHISQ`](#op-test_chisq) | Checks whether two categorical fields are associated by comparing a cross-tabulation with the counts expected if unrelated. | Is preferred channel associated with age band? | intermediate | [`TEST_FISHER_EXACT`](#op-test_fisher_exact) when the table is 2x2 and some expected counts are small.<br>[`TEST_PROP_Z`](#op-test_prop_z) when you compare a yes/no rate between exactly two groups and want a confidence interval.<br>[`TEST_SPEARMAN_R`](#op-test_spearman_r) when both fields are numeric.<br>[`OVERLAY_CHISQ_VS_POP`](overlay.md#op-overlay_chisq_vs_pop) when you compare one subgroup's mix with the whole population. |
| [`TEST_FISHER_EXACT`](#op-test_fisher_exact) | Exact test of whether two yes/no fields are associated, built for small 2x2 tables. | In a small pilot, did the treated group recover more often than the control group? | intermediate | [`TEST_CHISQ`](#op-test_chisq) when either field has more than two categories.<br>[`TEST_PROP_Z`](#op-test_prop_z) when counts are large and you want the difference in rates with a confidence interval. |
| [`TEST_KENDALL_TAU`](#op-test_kendall_tau) | Measures how often pairs of rows agree in order on two numeric fields: a rank-based link suited to small samples and ties. | In a small panel, do judges who score one entry higher also score the other higher? | intermediate | [`TEST_SPEARMAN_R`](#op-test_spearman_r) when the data are large; its cost grows with the square of the row count.<br>[`TEST_PEARSON_R`](#op-test_pearson_r) when you want the strength of a straight-line link.<br>[`TEST_TREND`](#op-test_trend) when you want to know whether one ordered series keeps rising or falling. |
| [`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) | Rank-based check of whether values tend to be larger in some groups than in others, across two or more groups. | Do satisfaction ratings tend to differ across the four regions? | intermediate | [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when the data are roughly normal and you want to compare averages.<br>[`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when there are only two groups.<br>[`TEST_ANOVA_RM`](#op-test_anova_rm) when the same subjects are measured in every group and the measure is roughly normal (Pulse has no Friedman test for skewed repeated measures; for two conditions use TEST_WILCOXON_SR). |
| [`TEST_KS`](#op-test_ks) | Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape. | Do order values in the two regions have the same overall distribution? | intermediate | [`TEST_SHAPIRO_WILK`](#op-test_shapiro_wilk) when you want to check one field against the normal distribution.<br>[`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when you only care whether one group's values tend to be larger.<br>[`OVERLAY_KS_VS_POP`](overlay.md#op-overlay_ks_vs_pop) when you compare a subgroup with the whole population. |
| [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) | Rank-based check of whether values in one of two groups tend to be larger than in the other. | Do customers on the new plan tend to rate the service higher? | intermediate | [`TEST_WILCOXON_SR`](#op-test_wilcoxon_sr) when the same subjects are measured twice.<br>[`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when there are three or more groups.<br>[`TEST_WELCH`](#op-test_welch) when the data are roughly normal and you want the difference in averages.<br>[`TEST_KS`](#op-test_ks) when you care about any difference in the shape of the two distributions. |
| [`TEST_PAIRED_T`](#op-test_paired_t) | Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero. | On average, did customers' spend differ between the period before and after the loyalty programme started? | intermediate | [`TEST_WELCH`](#op-test_welch) when the two sets of values come from different, unrelated subjects.<br>[`TEST_WILCOXON_SR`](#op-test_wilcoxon_sr) when the per-row differences are skewed or have extreme values.<br>[`TEST_ANOVA_RM`](#op-test_anova_rm) when each subject is measured under three or more conditions. |
| [`TEST_PEARSON_R`](#op-test_pearson_r) | Measures how strongly two numeric fields rise and fall together along a straight line. | Do customers who spend more also visit more often? | intermediate | [`TEST_SPEARMAN_R`](#op-test_spearman_r) when the link is consistent but curved, or the values are ranks.<br>[`TEST_KENDALL_TAU`](#op-test_kendall_tau) when the sample is small or has many tied values.<br>[`TEST_CHISQ`](#op-test_chisq) when both fields are categories.<br>[`REG_OLS`](regression.md#op-reg_ols) when you want to predict one field from several others.<br>[`TEST_TREND`](#op-test_trend) when you want to know whether one ordered series keeps rising or falling. |
| [`TEST_PROP_Z`](#op-test_prop_z) | Checks whether the rate of one outcome, such as conversion, differs between two groups. | Does the new checkout convert at a different rate from the old one? | basic | [`TEST_FISHER_EXACT`](#op-test_fisher_exact) when some groups have only a handful of successes or failures.<br>[`TEST_CHISQ`](#op-test_chisq) when there are more than two groups or more than two outcomes.<br>[`TEST_WELCH`](#op-test_welch) when the outcome is a numeric measure rather than a yes/no rate. |
| [`TEST_SHAPIRO_WILK`](#op-test_shapiro_wilk) | Checks whether a numeric field looks normally distributed, overall or within each group. | Is response time roughly bell-shaped, or clearly skewed or heavy-tailed? | intermediate | [`TEST_KS`](#op-test_ks) when you compare the distributions of two groups with each other.<br>[`TEST_BROWN_FORSYTHE`](#op-test_brown_forsythe) when you want to check whether groups have equal spread. |
| [`TEST_SPEARMAN_R`](#op-test_spearman_r) | Measures how consistently one numeric field rises (or falls) as the other rises, using ranks, so the link need not be a straight line. | Do higher-ranked products also tend to sell more, even if not in proportion? | intermediate | [`TEST_PEARSON_R`](#op-test_pearson_r) when you want the strength of a straight-line link.<br>[`TEST_KENDALL_TAU`](#op-test_kendall_tau) when the sample is small or many values are tied.<br>[`TEST_CHISQ`](#op-test_chisq) when both fields are categories.<br>[`TEST_TREND`](#op-test_trend) when you want to know whether one ordered series keeps rising or falling. |
| [`TEST_T`](#op-test_t) | Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version). | Is the average order value different from our target of 50? | intermediate | [`TEST_PAIRED_T`](#op-test_paired_t) when the same subjects are measured twice, such as before and after.<br>[`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when two groups are compared and the measure is heavily skewed, has extreme values or is only ordered.<br>[`TEST_ANOVA_WELCH`](#op-test_anova_welch) when there are three or more groups.<br>[`TEST_PROP_Z`](#op-test_prop_z) when the outcome is a yes/no rate rather than a numeric measure.<br>[`TEST_WELCH`](#op-test_welch) when the request is always a two-group comparison and should say so by name.<br>[`TEST_Z_TWO_SAMPLE`](#op-test_z_two_sample) when both groups are large and reporting conventions call for a z-test. |
| [`TEST_TREND`](#op-test_trend) | Checks whether an ordered series, such as monthly totals, tends to keep rising or keep falling. | Is monthly churn creeping up? | intermediate | [`REG_OLS`](regression.md#op-reg_ols) when you want the size of a straight-line slope.<br>[`TEST_WELCH`](#op-test_welch) when you compare the averages of two specific periods.<br>[`TEST_KENDALL_TAU`](#op-test_kendall_tau) when you relate two numeric fields rather than one series in order. |
| [`TEST_TUKEY_HSD`](#op-test_tukey_hsd) | After an ANOVA, compares every pair of group averages to find which ones differ, keeping the overall false-alarm rate in check. | Which regions differ from each other in average spend? | advanced | [`TEST_ANOVA_F`](#op-test_anova_f) when you only need to know whether any group differs.<br>[`TEST_WELCH`](#op-test_welch) when groups have clearly unequal spread: run pairwise Welch tests with multiplicity set on the request to adjust for multiple comparisons.<br>[`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the measure is heavily skewed or only ordered: run pairwise Mann-Whitney tests with multiplicity set on the request to adjust for multiple comparisons. |
| [`TEST_WELCH`](#op-test_welch) | Checks whether the average of a numeric field differs between two groups, without assuming they have equal spread. | Do customers in the two pricing arms spend different amounts on average? | intermediate | [`TEST_PAIRED_T`](#op-test_paired_t) when the same subjects are measured twice, such as before and after.<br>[`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the measure is heavily skewed, has extreme values or is only ordered.<br>[`TEST_ANOVA_WELCH`](#op-test_anova_welch) when there are three or more groups.<br>[`TEST_T`](#op-test_t) when you compare one group's average with a fixed target value.<br>[`TEST_KS`](#op-test_ks) when you care about the whole shape of the two distributions, not just the average. |
| [`TEST_WILCOXON_SR`](#op-test_wilcoxon_sr) | Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve. | Did each customer's rating tend to go up after the redesign? | intermediate | [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the two sets of values come from different, unrelated subjects.<br>[`TEST_PAIRED_T`](#op-test_paired_t) when the per-row differences are roughly normal and you want the average change. |
| [`TEST_Z_TWO_SAMPLE`](#op-test_z_two_sample) | Large-sample check of whether two groups differ in average, with the p-value read from the normal distribution. | In a large survey, do the two regions differ in average score? | intermediate | [`TEST_WELCH`](#op-test_welch) when either group is small; the t-distribution p-value is more honest there.<br>[`TEST_PROP_Z`](#op-test_prop_z) when the outcome is a yes/no rate.<br>[`TEST_PAIRED_T`](#op-test_paired_t) when the same subjects are measured twice. |

## Operators

<a id="op-test_anova_f"></a>

### `TEST_ANOVA_F`

Checks whether the average of a numeric measure differs across three or more groups.

**Level:** intermediate

**Also known as:** `anova`, `one-way anova`, `aov`

**Questions it answers:**

- Does average spend differ across regions?
- Do the treatment arms produce different average outcomes?

**Use cases by domain:**

- *survey:* Compare mean satisfaction across age bands.
- *ops:* Compare average delivery time across warehouses.
- *science:* Compare mean yield across fertiliser treatments.

**Assumptions:**

- Rows are independent of each other, within and across groups.
- The measure is roughly normally distributed within each group.
- Groups have similar variances; TEST_BROWN_FORSYTHE checks this.
- It is an overall test: it says whether any group average stands apart, not which one.

**Use something else:**

- [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when the groups have clearly unequal spread.
- [`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when the measure is heavily skewed or only ordered.
- [`TEST_ANOVA_RM`](#op-test_anova_rm) when the same subjects are measured in every group.
- [`TEST_WELCH`](#op-test_welch) when there are only two groups.
- [`TEST_TUKEY_HSD`](#op-test_tukey_hsd) when you need to know which pairs of groups differ.

**Follow up with:**

- [`TEST_TUKEY_HSD`](#op-test_tukey_hsd) when the groups differ and you want to know which pairs of groups do.
- [`OVERLAY_PAIRWISE_WELCH_T`](overlay.md#op-overlay_pairwise_welch_t) when the groups are the rows of a crosstab of averages and you want every pair tested.

**Glossary:** [`effect-size`](../glossary.md#term-effect-size), [`eta-squared`](../glossary.md#term-eta-squared), [`f-statistic`](../glossary.md#term-f-statistic), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`omega-squared`](../glossary.md#term-omega-squared), [`p-value`](../glossary.md#term-p-value), [`post-hoc-test`](../glossary.md#term-post-hoc-test), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-test-anova-f`](../skills/op-test-anova-f.md)

<a id="op-test_anova_rm"></a>

### `TEST_ANOVA_RM`

Checks whether the average differs across conditions when every subject is measured under each condition.

**Level:** advanced

**Also known as:** `repeated measures anova`, `within-subjects anova`

**Questions it answers:**

- Do the same panel members rate the three ad concepts differently on average?
- Does each patient's score change across the baseline, mid-point and final visits?

**Use cases by domain:**

- *survey:* Compare each respondent's ratings of several brands asked in one survey.
- *ops:* Compare each store's sales across three promotion periods.
- *science:* Compare each subject's response across repeated measurement sessions.

**Assumptions:**

- The same subjects (SubjectField) are measured once under every condition; subjects missing a condition are dropped.
- Subjects are independent of each other.
- The measure is roughly normally distributed within each condition.
- Sphericity: differences between each pair of conditions are about equally variable. No correction is applied, so violations make the p-value too small.

**Use something else:**

- [`TEST_ANOVA_F`](#op-test_anova_f) when each group holds different, unrelated subjects.
- [`TEST_PAIRED_T`](#op-test_paired_t) when there are only two conditions.

**Glossary:** [`f-statistic`](../glossary.md#term-f-statistic), [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`partial-eta-squared`](../glossary.md#term-partial-eta-squared), [`repeated-measures`](../glossary.md#term-repeated-measures), [`sphericity`](../glossary.md#term-sphericity)

**Skill:** [`op-test-anova-rm`](../skills/op-test-anova-rm.md)

<a id="op-test_anova_welch"></a>

### `TEST_ANOVA_WELCH`

Checks whether the average of a numeric measure differs across groups when the groups may have unequal spread.

**Level:** intermediate

**Also known as:** `welch's anova`, `oneway.test`

**Questions it answers:**

- Does average response time differ across regions whose variability is very different?
- Do the store formats differ in average basket size, given some formats are far more variable?

**Use cases by domain:**

- *survey:* Compare mean scores across segments of very different sizes and spreads.
- *ops:* Compare average processing time across sites with uneven variability.
- *science:* Compare treatment means when the spread grows with the dose.

**Assumptions:**

- Rows are independent of each other, within and across groups.
- The measure is roughly normally distributed within each group.
- Groups may have unequal variances; each group needs some spread (a constant group is refused).
- It is an overall test: it says whether any group average stands apart, not which one.

**Use something else:**

- [`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when the measure is heavily skewed or only ordered.
- [`TEST_ANOVA_RM`](#op-test_anova_rm) when the same subjects are measured in every group.
- [`TEST_WELCH`](#op-test_welch) when there are only two groups, or you want pairwise follow-ups: run TEST_WELCH per pair and set multiplicity on the request (for example holm) to adjust their p-values for multiple comparisons.
- [`TEST_ANOVA_F`](#op-test_anova_f) when spreads are similar and you want Tukey's pairwise follow-up.

**Follow up with:**

- [`OVERLAY_PAIRWISE_WELCH_T`](overlay.md#op-overlay_pairwise_welch_t) when the groups differ and you want each pair tested without assuming equal spread.
- [`TEST_TUKEY_HSD`](#op-test_tukey_hsd) when the groups differ and their spreads turn out similar after all.

**Glossary:** [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`f-statistic`](../glossary.md#term-f-statistic), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`normal-distribution`](../glossary.md#term-normal-distribution), [`omega-squared`](../glossary.md#term-omega-squared), [`p-value`](../glossary.md#term-p-value), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-test-anova-welch`](../skills/op-test-anova-welch.md)

<a id="op-test_brown_forsythe"></a>

### `TEST_BROWN_FORSYTHE`

Checks whether the spread of a numeric field differs across groups: a robust test of equal variances.

**Level:** advanced

**Also known as:** `levene's test`, `median-centred levene test`

**Questions it answers:**

- Is delivery time more variable at some warehouses than at others?
- How different is the spread of the measure across groups?

**Use cases by domain:**

- *survey:* Check whether some segments answer far more variably than others.
- *ops:* Compare the consistency of processing times across sites.
- *science:* Check whether measurement spread differs across treatment groups.

**Assumptions:**

- Rows are independent of each other, within and across groups.
- It measures distance from each group's median, so it stays reliable when the data are not normal.
- Very small groups make the medians unstable.
- A large p-value is not evidence that the spreads are equal; many analysts simply use Welch's tests rather than pre-testing.

**Use something else:**

- [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when you want to compare the group averages themselves.
- [`TEST_SHAPIRO_WILK`](#op-test_shapiro_wilk) when you want to check whether a measure is bell-shaped.

**Follow up with:**

- [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when the spreads differ and you still want to compare group averages.

**Glossary:** [`f-statistic`](../glossary.md#term-f-statistic), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`median`](../glossary.md#term-median), [`p-value`](../glossary.md#term-p-value), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-test-brown-forsythe`](../skills/op-test-brown-forsythe.md)

<a id="op-test_chisq"></a>

### `TEST_CHISQ`

Checks whether two categorical fields are associated by comparing a cross-tabulation with the counts expected if unrelated.

**Level:** intermediate

**Also known as:** `chi square`, `chi-square test`, `pearson chi-square`, `chisq.test`

**Questions it answers:**

- Is preferred channel associated with age band?
- Is the mix of plan types different across regions?

**Use cases by domain:**

- *survey:* Check whether brand preference varies by region.
- *ops:* Check whether ticket category is associated with the support channel.
- *science:* Check whether outcome category depends on treatment arm.

**Assumptions:**

- Each row is counted once and rows are independent of each other.
- It works on raw counts, never on percentages or averages.
- Expected counts should mostly be 5 or more (Cochran's rule: none below 1 and no more than a fifth below 5); below that the p-value is unreliable, and Pulse warns when any cell is under 5.
- No continuity correction is applied.

**Use something else:**

- [`TEST_FISHER_EXACT`](#op-test_fisher_exact) when the table is 2x2 and some expected counts are small.
- [`TEST_PROP_Z`](#op-test_prop_z) when you compare a yes/no rate between exactly two groups and want a confidence interval.
- [`TEST_SPEARMAN_R`](#op-test_spearman_r) when both fields are numeric.
- [`OVERLAY_CHISQ_VS_POP`](overlay.md#op-overlay_chisq_vs_pop) when you compare one subgroup's mix with the whole population.

**Follow up with:**

- [`OVERLAY_FISHER_EXACT_CELL`](overlay.md#op-overlay_fisher_exact_cell) when the table shows an association and you want to see which cells drive it.
- [`OVERLAY_CHISQ_ROW`](overlay.md#op-overlay_chisq_row) when you want to see which rows depart from the overall column mix.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`continuity-correction`](../glossary.md#term-continuity-correction), [`cramers-v`](../glossary.md#term-cramers-v), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`independence`](../glossary.md#term-independence), [`p-value`](../glossary.md#term-p-value), [`phi`](../glossary.md#term-phi)

**Skill:** [`op-test-chisq`](../skills/op-test-chisq.md)

<a id="op-test_fisher_exact"></a>

### `TEST_FISHER_EXACT`

Exact test of whether two yes/no fields are associated, built for small 2x2 tables.

**Level:** intermediate

**Also known as:** `fisher's exact test`, `fisher.test`

**Questions it answers:**

- In a small pilot, did the treated group recover more often than the control group?
- With only a few dozen responses, is opting in associated with the sign-up channel?

**Use cases by domain:**

- *survey:* Compare a yes/no answer between two small subgroups.
- *ops:* Compare a rare failure rate between two production lines.
- *science:* Compare a binary outcome between two small treatment arms.

**Assumptions:**

- Rows are independent of each other, and each row is counted once.
- Strictly 2x2: both fields must have exactly two levels.
- Row and column totals are treated as fixed, which makes its p-values tend to run a little large, so it misses real associations somewhat more often than it needs to.
- The two-sided p-value sums every table no more likely than the one observed; other tools sometimes double a one-sided tail and disagree slightly.

**Use something else:**

- [`TEST_CHISQ`](#op-test_chisq) when either field has more than two categories.
- [`TEST_PROP_Z`](#op-test_prop_z) when counts are large and you want the difference in rates with a confidence interval.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`exact-test`](../glossary.md#term-exact-test), [`independence`](../glossary.md#term-independence), [`odds-ratio`](../glossary.md#term-odds-ratio), [`p-value`](../glossary.md#term-p-value), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-fisher-exact`](../skills/op-test-fisher-exact.md)

<a id="op-test_kendall_tau"></a>

### `TEST_KENDALL_TAU`

Measures how often pairs of rows agree in order on two numeric fields: a rank-based link suited to small samples and ties.

**Level:** intermediate

**Also known as:** `kendall`, `kendall's tau`, `tau-b`

**Questions it answers:**

- In a small panel, do judges who score one entry higher also score the other higher?
- Do two coarse rating scales tend to agree?

**Use cases by domain:**

- *survey:* Relate two short rating scales with many tied answers.
- *ops:* Check whether two priority rankings agree.
- *science:* Relate two measures in a small study.

**Assumptions:**

- Pairs of values are independent of each other.
- It detects monotonic links; ties in either field are corrected for (tau-b).
- The p-value is a two-sided normal approximation.

**Use something else:**

- [`TEST_SPEARMAN_R`](#op-test_spearman_r) when the data are large; its cost grows with the square of the row count.
- [`TEST_PEARSON_R`](#op-test_pearson_r) when you want the strength of a straight-line link.
- [`TEST_TREND`](#op-test_trend) when you want to know whether one ordered series keeps rising or falling.

**Follow up with:**

- [`REG_OLS`](regression.md#op-reg_ols) when the link looks roughly straight and you want to predict one field from the other.

**Glossary:** [`correlation`](../glossary.md#term-correlation), [`independence`](../glossary.md#term-independence), [`kendall-tau`](../glossary.md#term-kendall-tau), [`monotonic-trend`](../glossary.md#term-monotonic-trend), [`p-value`](../glossary.md#term-p-value), [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-kendall-tau`](../skills/op-test-kendall-tau.md)

<a id="op-test_kruskal_wallis"></a>

### `TEST_KRUSKAL_WALLIS`

Rank-based check of whether values tend to be larger in some groups than in others, across two or more groups.

**Level:** intermediate

**Also known as:** `kruskal-wallis h test`, `kruskal.test`

**Questions it answers:**

- Do satisfaction ratings tend to differ across the four regions?
- Do skewed repair times differ across product lines?

**Use cases by domain:**

- *survey:* Compare 1-5 ratings across several customer segments.
- *ops:* Compare skewed wait times across branches.
- *science:* Compare a non-normal outcome across several treatment groups.

**Assumptions:**

- Rows are independent of each other, within and across groups.
- It tests whether values tend to be larger in some groups; it reads as a difference in medians only when all groups have the same shape.
- It is an overall test: to find which groups differ, follow up with pairwise Mann-Whitney tests and set multiplicity on the request to adjust them for multiple comparisons.
- The p-value comes from a chi-square approximation that is shaky for very small groups.

**Use something else:**

- [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when the data are roughly normal and you want to compare averages.
- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when there are only two groups.
- [`TEST_ANOVA_RM`](#op-test_anova_rm) when the same subjects are measured in every group and the measure is roughly normal (Pulse has no Friedman test for skewed repeated measures; for two conditions use TEST_WILCOXON_SR).

**Follow up with:**

- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the groups differ and you want a rank-based comparison of each pair, read with multiple-comparison caution.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`epsilon-squared`](../glossary.md#term-epsilon-squared), [`independence`](../glossary.md#term-independence), [`median`](../glossary.md#term-median), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`non-parametric`](../glossary.md#term-non-parametric), [`p-value`](../glossary.md#term-p-value), [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-test-kruskal-wallis`](../skills/op-test-kruskal-wallis.md)

<a id="op-test_ks"></a>

### `TEST_KS`

Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape.

**Level:** intermediate

**Also known as:** `kolmogorov-smirnov test`, `ks test`, `ks.test`

**Questions it answers:**

- Do order values in the two regions have the same overall distribution?
- Has the shape of response times changed between the old and new system?

**Use cases by domain:**

- *survey:* Compare the full spread of scores between two survey waves.
- *ops:* Detect a change in the shape of latency between two releases.
- *science:* Compare measurement distributions between two instruments.

**Assumptions:**

- Rows are independent of each other, within and across the two groups.
- It compares two observed groups; it is not a normality test with parameters estimated from the data (that needs the Lilliefors correction).
- With few rows per group it has low power; with very large groups even trivial differences in shape are flagged.
- The p-value is a two-sided large-sample approximation; many tied values make it conservative.

**Use something else:**

- [`TEST_SHAPIRO_WILK`](#op-test_shapiro_wilk) when you want to check one field against the normal distribution.
- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when you only care whether one group's values tend to be larger.
- [`OVERLAY_KS_VS_POP`](overlay.md#op-overlay_ks_vs_pop) when you compare a subgroup with the whole population.

**Glossary:** [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`independence`](../glossary.md#term-independence), [`non-parametric`](../glossary.md#term-non-parametric), [`p-value`](../glossary.md#term-p-value), [`statistical-power`](../glossary.md#term-statistical-power), [`statistical-significance`](../glossary.md#term-statistical-significance), [`test-statistic`](../glossary.md#term-test-statistic), [`ties`](../glossary.md#term-ties), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-ks`](../skills/op-test-ks.md)

<a id="op-test_mann_whitney_u"></a>

### `TEST_MANN_WHITNEY_U`

Rank-based check of whether values in one of two groups tend to be larger than in the other.

**Level:** intermediate

**Also known as:** `mann-whitney`, `wilcoxon rank-sum test`

**Questions it answers:**

- Do customers on the new plan tend to rate the service higher?
- Do response times at one site tend to run longer than at the other?

**Use cases by domain:**

- *survey:* Compare 1-5 ratings between two segments.
- *ops:* Compare skewed resolution times between two support teams.
- *science:* Compare a non-normal outcome between treatment and control.

**Assumptions:**

- Rows are independent of each other, within and across the two groups.
- It tests whether a value from one group tends to exceed one from the other; it reads as a difference in medians only when both groups have the same shape.
- The p-value is a two-sided large-sample approximation with tie and continuity corrections, not an exact value.

**Use something else:**

- [`TEST_WILCOXON_SR`](#op-test_wilcoxon_sr) when the same subjects are measured twice.
- [`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when there are three or more groups.
- [`TEST_WELCH`](#op-test_welch) when the data are roughly normal and you want the difference in averages.
- [`TEST_KS`](#op-test_ks) when you care about any difference in the shape of the two distributions.

**Glossary:** [`continuity-correction`](../glossary.md#term-continuity-correction), [`independence`](../glossary.md#term-independence), [`median`](../glossary.md#term-median), [`non-parametric`](../glossary.md#term-non-parametric), [`p-value`](../glossary.md#term-p-value), [`rank`](../glossary.md#term-rank), [`rank-biserial`](../glossary.md#term-rank-biserial), [`test-statistic`](../glossary.md#term-test-statistic), [`ties`](../glossary.md#term-ties), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-mann-whitney-u`](../skills/op-test-mann-whitney-u.md)

<a id="op-test_paired_t"></a>

### `TEST_PAIRED_T`

Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero.

**Level:** intermediate

**Also known as:** `paired t test`, `paired-samples t test`, `dependent t test`

**Questions it answers:**

- On average, did customers' spend differ between the period before and after the loyalty programme started?
- Do patients' scores differ between the first and second visit?

**Use cases by domain:**

- *survey:* Compare each respondent's rating of two products in the same questionnaire.
- *ops:* Compare each store's weekly sales before and after a layout change.
- *science:* Compare each subject's measurement before and after treatment.

**Assumptions:**

- Each row pairs two measurements of the same subject, held side by side in Field and Field2.
- Pairs are independent of each other.
- The per-row differences are roughly normally distributed; the two fields themselves need not be.
- Rows missing either value are dropped. The p-value is two-sided.

**Use something else:**

- [`TEST_WELCH`](#op-test_welch) when the two sets of values come from different, unrelated subjects.
- [`TEST_WILCOXON_SR`](#op-test_wilcoxon_sr) when the per-row differences are skewed or have extreme values.
- [`TEST_ANOVA_RM`](#op-test_anova_rm) when each subject is measured under three or more conditions.

**Glossary:** [`cohens-d`](../glossary.md#term-cohens-d), [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`paired-data`](../glossary.md#term-paired-data), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-paired-t`](../skills/op-test-paired-t.md)

<a id="op-test_pearson_r"></a>

### `TEST_PEARSON_R`

Measures how strongly two numeric fields rise and fall together along a straight line.

**Level:** intermediate

**Also known as:** `pearson`, `pearson correlation`, `pearson's r`

**Questions it answers:**

- Do customers who spend more also visit more often?
- Does study time go with higher test scores?

**Use cases by domain:**

- *survey:* Check whether satisfaction tracks likelihood to recommend.
- *ops:* See whether ad spend moves with weekly revenue.
- *science:* Relate dose to measured response.

**Assumptions:**

- The relationship is roughly a straight line.
- Pairs of values are independent of each other.
- A few extreme values can dominate the result.
- The p-value assumes both fields are roughly normal; with small samples that matters most.
- r is the covariance of the two fields (details.covariance, dividing by n - 1) divided by both sample standard deviations (the square roots of details.variance_x and variance_y, also n - 1), which makes it unit-free; AGG_STDDEV divides by n, so it does not reproduce r.

**Use something else:**

- [`TEST_SPEARMAN_R`](#op-test_spearman_r) when the link is consistent but curved, or the values are ranks.
- [`TEST_KENDALL_TAU`](#op-test_kendall_tau) when the sample is small or has many tied values.
- [`TEST_CHISQ`](#op-test_chisq) when both fields are categories.
- [`REG_OLS`](regression.md#op-reg_ols) when you want to predict one field from several others.
- [`TEST_TREND`](#op-test_trend) when you want to know whether one ordered series keeps rising or falling.

**Follow up with:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want to predict one field from the other, or add more predictors.

**Glossary:** [`confidence-interval`](../glossary.md#term-confidence-interval), [`correlation`](../glossary.md#term-correlation), [`covariance`](../glossary.md#term-covariance), [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`outlier`](../glossary.md#term-outlier), [`p-value`](../glossary.md#term-p-value), [`r-squared`](../glossary.md#term-r-squared), [`standard-deviation`](../glossary.md#term-standard-deviation)

**Skill:** [`op-test-pearson-r`](../skills/op-test-pearson-r.md)

<a id="op-test_prop_z"></a>

### `TEST_PROP_Z`

Checks whether the rate of one outcome, such as conversion, differs between two groups.

**Level:** basic

**Also known as:** `two-proportion z test`, `prop.test`

**Questions it answers:**

- Does the new checkout convert at a different rate from the old one?
- Is the share of promoters different between the two regions?

**Use cases by domain:**

- *survey:* Compare the share answering yes between two segments.
- *ops:* Compare conversion rates between two experiment arms.
- *science:* Compare response rates between treatment and control.

**Assumptions:**

- Rows are independent of each other, within and across the two groups.
- Each group needs at least 10 successes and 10 failures for the normal approximation.
- The p-value is two-sided; the confidence interval on the rate difference is the simple Wald interval, which is rough near 0% or 100%.

**Use something else:**

- [`TEST_FISHER_EXACT`](#op-test_fisher_exact) when some groups have only a handful of successes or failures.
- [`TEST_CHISQ`](#op-test_chisq) when there are more than two groups or more than two outcomes.
- [`TEST_WELCH`](#op-test_welch) when the outcome is a numeric measure rather than a yes/no rate.

**Glossary:** [`cohens-h`](../glossary.md#term-cohens-h), [`confidence-interval`](../glossary.md#term-confidence-interval), [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`two-tailed`](../glossary.md#term-two-tailed), [`z-score`](../glossary.md#term-z-score)

**Skill:** [`op-test-prop-z`](../skills/op-test-prop-z.md)

<a id="op-test_shapiro_wilk"></a>

### `TEST_SHAPIRO_WILK`

Checks whether a numeric field looks normally distributed, overall or within each group.

**Level:** intermediate

**Also known as:** `shapiro test`, `shapiro.test`

**Questions it answers:**

- Is response time roughly bell-shaped, or clearly skewed or heavy-tailed?
- How far do scores in each group depart from a normal shape?

**Use cases by domain:**

- *survey:* Describe how far an index score departs from a normal shape, alongside a plot of it.
- *ops:* Check whether processing times are bell-shaped or heavily skewed.
- *science:* Check the normality assumption per treatment group.

**Assumptions:**

- Rows are independent of each other.
- Needs at least 3 values per group; the p-value is only calibrated for 5 to 5000 values, so treat it as advisory below 5 or above 5000 (Pulse warns).
- With few rows it has low power and can miss real departures; with very large samples even trivial departures are flagged, so look at the shape too.
- Pulse computes the Shapiro-Francia form, a close approximation to Shapiro-Wilk; with SplitBy the headline is the group that departs most.

**Use something else:**

- [`TEST_KS`](#op-test_ks) when you compare the distributions of two groups with each other.
- [`TEST_BROWN_FORSYTHE`](#op-test_brown_forsythe) when you want to check whether groups have equal spread.

**Follow up with:**

- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the shape is far from normal and you planned a two-group t-test.
- [`TEST_KRUSKAL_WALLIS`](#op-test_kruskal_wallis) when the shape is far from normal and you planned an ANOVA across three or more groups.

**Glossary:** [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`independence`](../glossary.md#term-independence), [`kurtosis`](../glossary.md#term-kurtosis), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`skew`](../glossary.md#term-skew), [`statistical-power`](../glossary.md#term-statistical-power), [`statistical-significance`](../glossary.md#term-statistical-significance), [`test-statistic`](../glossary.md#term-test-statistic)

**Skill:** [`op-test-shapiro-wilk`](../skills/op-test-shapiro-wilk.md)

<a id="op-test_spearman_r"></a>

### `TEST_SPEARMAN_R`

Measures how consistently one numeric field rises (or falls) as the other rises, using ranks, so the link need not be a straight line.

**Level:** intermediate

**Also known as:** `spearman`, `spearman's rho`, `spearman rank correlation`

**Questions it answers:**

- Do higher-ranked products also tend to sell more, even if not in proportion?
- Does satisfaction tend to rise with tenure?

**Use cases by domain:**

- *survey:* Relate two rating-scale answers to each other.
- *ops:* Check whether skewed order size tends to grow with customer age.
- *science:* Relate dose to a response that levels off.

**Assumptions:**

- Pairs of values are independent of each other.
- It detects monotonic links (one keeps rising as the other rises, or falls); a U-shaped link can score near zero.
- The p-value uses a t approximation that weakens with small samples or many tied values.

**Use something else:**

- [`TEST_PEARSON_R`](#op-test_pearson_r) when you want the strength of a straight-line link.
- [`TEST_KENDALL_TAU`](#op-test_kendall_tau) when the sample is small or many values are tied.
- [`TEST_CHISQ`](#op-test_chisq) when both fields are categories.
- [`TEST_TREND`](#op-test_trend) when you want to know whether one ordered series keeps rising or falling.

**Follow up with:**

- [`REG_OLS`](regression.md#op-reg_ols) when the link looks roughly straight and you want to predict one field from the other.

**Glossary:** [`correlation`](../glossary.md#term-correlation), [`independence`](../glossary.md#term-independence), [`monotonic-trend`](../glossary.md#term-monotonic-trend), [`outlier`](../glossary.md#term-outlier), [`p-value`](../glossary.md#term-p-value), [`rank`](../glossary.md#term-rank), [`spearman-rho`](../glossary.md#term-spearman-rho), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-test-spearman-r`](../skills/op-test-spearman-r.md)

<a id="op-test_t"></a>

### `TEST_T`

Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version).

**Level:** intermediate

**Also known as:** `t test`, `one-sample t test`

**Questions it answers:**

- Is the average order value different from our target of 50?
- Do new and returning customers spend different amounts on average?

**Use cases by domain:**

- *survey:* Compare mean satisfaction between two regions, or against a target score.
- *ops:* Check average handling time against a service-level target.
- *science:* Compare the mean response of a treatment group with a control group.

**Assumptions:**

- Rows are independent of each other, within and across groups.
- The two-group version uses Welch's correction, so the groups need not have equal variances.
- The average is roughly normally distributed: safe for large groups, risky for small skewed ones.
- The p-value is two-sided: it looks for a difference in either direction.

**Use something else:**

- [`TEST_PAIRED_T`](#op-test_paired_t) when the same subjects are measured twice, such as before and after.
- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when two groups are compared and the measure is heavily skewed, has extreme values or is only ordered.
- [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when there are three or more groups.
- [`TEST_PROP_Z`](#op-test_prop_z) when the outcome is a yes/no rate rather than a numeric measure.
- [`TEST_WELCH`](#op-test_welch) when the request is always a two-group comparison and should say so by name.
- [`TEST_Z_TWO_SAMPLE`](#op-test_z_two_sample) when both groups are large and reporting conventions call for a z-test.

**Glossary:** [`cohens-d`](../glossary.md#term-cohens-d), [`confidence-interval`](../glossary.md#term-confidence-interval), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-test-t`](../skills/op-test-t.md)

<a id="op-test_trend"></a>

### `TEST_TREND`

Checks whether an ordered series, such as monthly totals, tends to keep rising or keep falling.

**Level:** intermediate

**Also known as:** `mann-kendall test`

**Questions it answers:**

- Is monthly churn creeping up?
- Has weekly average wait time been falling over the year?

**Use cases by domain:**

- *survey:* Check whether wave-on-wave satisfaction keeps moving one way.
- *ops:* Check whether a smoothed daily error rate is drifting.
- *science:* Check whether yearly readings show a steady drift.

**Assumptions:**

- It runs on result rows (a post-test) in OrderBy order, such as a grouped or windowed series.
- Successive points are independent; seasonality or autocorrelation makes the p-value too small.
- It detects a monotonic trend, not a straight-line slope and not which periods differ.
- The p-value is a two-sided normal approximation; treat it as advisory below about 10 points (Pulse warns below 8).

**Use something else:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want the size of a straight-line slope.
- [`TEST_WELCH`](#op-test_welch) when you compare the averages of two specific periods.
- [`TEST_KENDALL_TAU`](#op-test_kendall_tau) when you relate two numeric fields rather than one series in order.

**Glossary:** [`independence`](../glossary.md#term-independence), [`kendall-tau`](../glossary.md#term-kendall-tau), [`monotonic-trend`](../glossary.md#term-monotonic-trend), [`non-parametric`](../glossary.md#term-non-parametric), [`p-value`](../glossary.md#term-p-value), [`test-statistic`](../glossary.md#term-test-statistic), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-trend`](../skills/op-test-trend.md)

<a id="op-test_tukey_hsd"></a>

### `TEST_TUKEY_HSD`

After an ANOVA, compares every pair of group averages to find which ones differ, keeping the overall false-alarm rate in check.

**Level:** advanced

**Also known as:** `tukey hsd`, `tukey's honest significant difference`

**Questions it answers:**

- Which regions differ from each other in average spend?
- Which treatment arms have different average outcomes?

**Use cases by domain:**

- *survey:* Find which age bands differ in mean satisfaction after an overall ANOVA.
- *ops:* Find which warehouses differ in average delivery time.
- *science:* Find which fertiliser treatments differ in mean yield.

**Assumptions:**

- Rows are independent of each other, within and across groups.
- Groups have similar variances, as in the standard ANOVA.
- It reads per-group averages and counts from result rows plus ms_within and df_within from a preceding TEST_ANOVA_F.
- Its p-values are already adjusted for multiple comparisons across every pair (they hold the family-wise error), using the studentized range (Tukey-Kramer for unequal group sizes); a request-level or instance multiplicity correction skips it, so they are never adjusted twice.

**Use something else:**

- [`TEST_ANOVA_F`](#op-test_anova_f) when you only need to know whether any group differs.
- [`TEST_WELCH`](#op-test_welch) when groups have clearly unequal spread: run pairwise Welch tests with multiplicity set on the request to adjust for multiple comparisons.
- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the measure is heavily skewed or only ordered: run pairwise Mann-Whitney tests with multiplicity set on the request to adjust for multiple comparisons.

**Glossary:** [`alpha`](../glossary.md#term-alpha), [`family-wise-error`](../glossary.md#term-family-wise-error), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`p-value`](../glossary.md#term-p-value), [`post-hoc-test`](../glossary.md#term-post-hoc-test), [`studentized-range`](../glossary.md#term-studentized-range)

**Skill:** [`op-test-tukey-hsd`](../skills/op-test-tukey-hsd.md)

<a id="op-test_welch"></a>

### `TEST_WELCH`

Checks whether the average of a numeric field differs between two groups, without assuming they have equal spread.

**Level:** intermediate

**Also known as:** `welch's t test`, `unequal variances t test`

**Questions it answers:**

- Do customers in the two pricing arms spend different amounts on average?
- Is average delivery time different between the two depots?

**Use cases by domain:**

- *survey:* Compare mean satisfaction between two customer segments.
- *ops:* Compare average order value between two sales channels.
- *science:* Compare the mean outcome of treatment and control groups of different sizes.

**Assumptions:**

- Rows are independent of each other, within and across the two groups.
- Each group's average is roughly normally distributed; large groups make this safe.
- Unequal variances are allowed: the degrees of freedom are adjusted (Welch-Satterthwaite).
- The p-value is two-sided.

**Use something else:**

- [`TEST_PAIRED_T`](#op-test_paired_t) when the same subjects are measured twice, such as before and after.
- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the measure is heavily skewed, has extreme values or is only ordered.
- [`TEST_ANOVA_WELCH`](#op-test_anova_welch) when there are three or more groups.
- [`TEST_T`](#op-test_t) when you compare one group's average with a fixed target value.
- [`TEST_KS`](#op-test_ks) when you care about the whole shape of the two distributions, not just the average.

**Glossary:** [`cohens-d`](../glossary.md#term-cohens-d), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-welch`](../skills/op-test-welch.md)

<a id="op-test_wilcoxon_sr"></a>

### `TEST_WILCOXON_SR`

Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve.

**Level:** intermediate

**Also known as:** `wilcoxon signed-rank test`, `signed-rank test`

**Questions it answers:**

- Did each customer's rating tend to go up after the redesign?
- Do patients' skewed symptom scores tend to fall between visits?

**Use cases by domain:**

- *survey:* Compare each respondent's ratings of two concepts on a short scale.
- *ops:* Compare each machine's skewed downtime before and after maintenance.
- *science:* Compare each subject's non-normal measurement before and after treatment.

**Assumptions:**

- Each row pairs two measurements of the same subject, held side by side in Field and Field2.
- Pairs are independent of each other.
- The differences are roughly symmetric around their centre; the test asks whether that centre is zero.
- Pairs whose values are equal are dropped; the p-value is a two-sided large-sample approximation and needs at least 6 pairs that differ.

**Use something else:**

- [`TEST_MANN_WHITNEY_U`](#op-test_mann_whitney_u) when the two sets of values come from different, unrelated subjects.
- [`TEST_PAIRED_T`](#op-test_paired_t) when the per-row differences are roughly normal and you want the average change.

**Glossary:** [`continuity-correction`](../glossary.md#term-continuity-correction), [`independence`](../glossary.md#term-independence), [`non-parametric`](../glossary.md#term-non-parametric), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`paired-data`](../glossary.md#term-paired-data), [`rank`](../glossary.md#term-rank), [`rank-biserial`](../glossary.md#term-rank-biserial), [`test-statistic`](../glossary.md#term-test-statistic), [`ties`](../glossary.md#term-ties), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-test-wilcoxon-sr`](../skills/op-test-wilcoxon-sr.md)

<a id="op-test_z_two_sample"></a>

### `TEST_Z_TWO_SAMPLE`

Large-sample check of whether two groups differ in average, with the p-value read from the normal distribution.

**Level:** intermediate

**Also known as:** `two-sample z test`

**Questions it answers:**

- In a large survey, do the two regions differ in average score?
- Across thousands of sessions, is average basket size different between the two app versions?

**Use cases by domain:**

- *survey:* Compare mean ratings between two large respondent groups where reporting conventions call for z.
- *ops:* Compare average spend between two high-traffic storefronts.
- *science:* Compare means of two large samples where the t and normal tails agree.

**Assumptions:**

- Rows are independent of each other, within and across the two groups.
- Both groups are large; with small groups the normal p-value comes out too small.
- Unequal variances are allowed: the standard error is the same as Welch's t-test.
- The p-value is two-sided.

**Use something else:**

- [`TEST_WELCH`](#op-test_welch) when either group is small; the t-distribution p-value is more honest there.
- [`TEST_PROP_Z`](#op-test_prop_z) when the outcome is a yes/no rate.
- [`TEST_PAIRED_T`](#op-test_paired_t) when the same subjects are measured twice.

**Glossary:** [`independence`](../glossary.md#term-independence), [`normal-distribution`](../glossary.md#term-normal-distribution), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size), [`standard-error`](../glossary.md#term-standard-error), [`two-tailed`](../glossary.md#term-two-tailed), [`variance`](../glossary.md#term-variance), [`z-score`](../glossary.md#term-z-score)

**Skill:** [`op-test-z-two-sample`](../skills/op-test-z-two-sample.md)
