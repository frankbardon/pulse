---
name: statistical-testing
description: Run tier-1 row tests (TEST_T, TEST_WELCH, TEST_Z_TWO_SAMPLE, TEST_CHISQ, TEST_ANOVA_F, TEST_ANOVA_WELCH, TEST_ANOVA_RM, TEST_KS, TEST_PAIRED_T, TEST_PROP_Z, TEST_PEARSON_R, TEST_SPEARMAN_R, TEST_KENDALL_TAU, TEST_MANN_WHITNEY_U, TEST_WILCOXON_SR, TEST_KRUSKAL_WALLIS, TEST_BROWN_FORSYTHE, TEST_FISHER_EXACT, TEST_SHAPIRO_WILK) and tier-2 post-tests (TEST_TUKEY_HSD, TEST_TREND, variants). Use when a request mentions hypothesis testing, p-values, ANOVA, t-tests, correlations, normality, or post-hoc comparisons.
type: guide
applies_to: process, compose, predict
---

# Statistical Testing

<skill_overview>
Pulse runs statistical tests inside the standard Process pipeline. Two tiers are addressed through a single request:

- **Tier 1 — row tests (`Request.Tests`).** Evaluated against the raw row stream alongside aggregators. Online-moments tests (`TEST_T`, `TEST_WELCH`, `TEST_CHISQ`, `TEST_ANOVA_F`) reuse the running `(mean, variance, n)` and contingency counts that aggregators already compute, so they add near-zero cost when their input fields overlap with active aggregations.
- **Tier 2 — post tests (`Request.PostTests`).** Evaluated after the window stage on the materialized result row set. Useful for ANOVA across grouper buckets, Tukey HSD post-hoc on per-group means, and trend tests over windowed series.

Both tiers share the same `Test` request shape and the same `TestResult` response shape. The difference is *what they consume*: tier 1 reads raw records, tier 2 reads result rows.
</skill_overview>

## Mental model

A single Process request can carry filters, features, attributes, aggregators, windows, **tests** (tier 1), and **post_tests** (tier 2). The pipeline runs in that order. Both tiers populate independent slots on the Response:

```json
{
  "tests": [
    {"type": "TEST_T", "field": "revenue", "split_by": "treatment", "alpha": 0.05}
  ],
  "post_tests": [
    {"type": "TEST_ANOVA_F", "field": "avg_revenue", "split_by": "region"}
  ]
}
```

Response:

```json
{
  "data": [ ... rows ... ],
  "tests":      [ ... tier-1 TestResult entries ... ],
  "post_tests": [ ... tier-2 TestResult entries ... ]
}
```

## When to use each tier

| Goal | Tier | Example |
|---|---|---|
| Treatment vs control on a continuous outcome | 1 | `TEST_T` on `revenue` split by `treatment` |
| Large-sample two-mean comparison (normal-CDF p) | 1 | `TEST_Z_TWO_SAMPLE` on `weighted_mean` split by `arm` |
| Independence of two categorical variables | 1 | `TEST_CHISQ` on `region × churned` |
| Compare means across k categories on raw rows | 1 | `TEST_ANOVA_F` on `revenue` split by `region` |
| Distribution comparison (CDF) | 1 | `TEST_KS` two-sample, raw rows |
| Compare aggregated per-group means | 2 | `TEST_ANOVA_F` on `avg_revenue` per `region` row |
| Pairwise comparison after a significant ANOVA | 2 | `TEST_TUKEY_HSD` on per-group means |
| Trend over an ordered series | 2 | `TEST_TREND` (Mann-Kendall) on `moving_avg_revenue` ordered by `period` |
| Correlate two per-group aggregates | 2 | `TEST_PEARSON_R` / `TEST_SPEARMAN_R` / `TEST_KENDALL_TAU` on result columns |
| Paired lift on pre/post result columns | 2 | `TEST_PAIRED_T` (parametric) or `TEST_WILCOXON_SR` (nonparametric) |
| Heteroscedastic ANOVA on per-group summaries | 2 | `TEST_ANOVA_WELCH` with `params.n_col`/`variance_col` |
| Two-sample CDF on result rows | 2 | `TEST_KS` over a `split_by` partition of result rows |
| Normality of a result column | 2 | `TEST_SHAPIRO_WILK` (optional `split_by` for per-group W) |
| Variance homogeneity on result rows | 2 | `TEST_BROWN_FORSYTHE` as pre-ANOVA gate |

If both tiers are populated, tier 1 runs during the row scan and tier 2 runs after windows. Either slot can be empty.

## Operator catalog

### TEST_T — one-sample or Welch two-sample t-test

Required fields:
- `field` — numeric field under test
- `split_by` — categorical that produces two groups (two-sample). Omit for one-sample.
- `params.mu` — hypothesized mean (one-sample only)

Output `TestResult.Details`:
- `groups` — group labels (two-sample)
- `n`, `mean`, `variance` — per-group moments
- `ci_low`, `ci_high` — two-sided confidence interval for the mean difference (two-sample) or for the mean (one-sample)
- `effect_size.cohens_d` — standardized effect size

Streamable: yes. Reuses online Welford moments.

### TEST_WELCH — explicit two-sample Welch t-test alias

Identical to `TEST_T` with `split_by` set. Provided so requests can document intent. Same Details payload.

### TEST_Z_TWO_SAMPLE — two-sample z-test on means

Large-sample variant of `TEST_WELCH`. Same Welch standard error (`√(va/na + vb/nb)`), same test statistic `(meanA − meanB) / SE`, but the p-value is the two-sided tail of the standard normal `Φ` instead of the Student-t `T_df`. No degrees of freedom; `DF` is reported as 0.

Use when:
- n is large per group (≥ ~50) AND the caller explicitly wants z-based inference (e.g. survey conventions where per-group variance is treated as known).
- Porting a workflow that already runs `scipy.stats.norm.cdf` on a `(m1 − m2) / √(v1/n1 + v2/n2)` statistic and you want byte-equal parity.

For small n the divergence from `TEST_WELCH` is non-trivial — at n=50 each group, p-values differ by ~0.005; at n≥200 the difference is <0.001. When in doubt, prefer `TEST_WELCH`.

Required fields:
- `field` — numeric measurement
- `split_by` — categorical producing exactly two groups

Output Details: same as `TEST_WELCH` (`groups`, `n`, `mean`, `variance`, `diff`, `ci_low`, `ci_high`, `effect_size.cohens_d`). Variant: `two_sample_normal`.

Streamable: yes. Reuses online Welford moments.

### TEST_CHISQ — chi-square independence

Required fields:
- `rows` — categorical row axis
- `cols` — categorical col axis

Output Details:
- `contingency` — observed counts as a 2D array
- `row_labels`, `col_labels`
- `expected_min` — smallest expected cell count (advisory)

Warning: `PULSE_TEST_EXPECTED_COUNT_TOO_LOW` when any expected count < 5.

Streamable: yes. Maintains running contingency counts.

### TEST_ANOVA_F — one-way ANOVA F-test

Required fields:
- `field` — numeric value
- `split_by` — categorical defining k ≥ 2 groups

Output Details:
- `groups`, `n`, `group_means`
- `ss_between`, `ss_within`, `df_between`, `df_within`
- `effect_size.eta_squared`

Streamable: yes. Online per-group moments.

### TEST_KS — Kolmogorov-Smirnov two-sample

Both tiers supported. Tier-2 variant `two_sample_post` runs the same Smirnov asymptotic p over result rows partitioned by `split_by` (exactly two groups).

Required fields:
- `field` — numeric value
- `split_by` — categorical producing exactly two groups

Streamable: **no** — sort-based ECDF, forces buffered path.

### TEST_TUKEY_HSD — Tukey's HSD post-hoc pairwise

Tier-2 only. Consumes per-group means and counts from upstream aggregator rows.

Required fields:
- `field` — numeric column on the result row (typically an aggregation alias)
- `split_by` — categorical column on the result row (typically a group alias)
- `params.ms_within` — within-group mean square (from a preceding `TEST_ANOVA_F` or computed externally)
- `params.df_within`

Output Details:
- `comparisons` — array of `{a, b, diff, q, p_adj, reject_null, ci_low, ci_high}`
- `k_groups`, `family_alpha`

Streamable: n/a (tier-2 always buffered).

### TEST_TREND — Mann-Kendall trend test

Required fields:
- `field` — numeric value
- `order_by` — ordering key(s) for the series

Output Details:
- `s` — Mann-Kendall sum statistic
- `var_s`
- `tau` — Kendall's tau

Streamable: no.

### TEST_MANN_WHITNEY_U — nonparametric two-sample

Nonparametric counterpart to `TEST_T` / `TEST_WELCH`. Buffered: combined-set mid-ranks with tie correction.

Required fields:
- `field` — numeric value
- `split_by` — categorical producing exactly two groups

Output Details:
- `groups`, `n` (per group)
- `u_a`, `u_b`, `u_min` — Mann-Whitney U statistics
- `r_a`, `r_b` — rank sums
- `mu_u`, `var_u`, `z` — asymptotic moments and the standardized statistic

Streamable: no (sort-based ECDF).

### TEST_WILCOXON_SR — Wilcoxon signed-rank (paired)

Both tiers supported. Nonparametric counterpart to `TEST_PAIRED_T`. Buffered: per-row diff, drop zero diffs, mid-rank `|diff|` with tie correction. Tier-2 variant `asymptotic_post` runs the same math over result-row pairs.

Required fields:
- `field` — numeric (after / post column)
- `field2` — numeric (before / pre column)

Output Details:
- `n` — number of non-zero paired observations after drop
- `w_plus`, `w_minus`, `mu_w`, `var_w`, `z`
- `zero_diffs` — count of exact-zero pairs dropped

Streamable: no.

### TEST_KRUSKAL_WALLIS — nonparametric k-group

Nonparametric counterpart to `TEST_ANOVA_F`. Buffered: combined-set mid-ranks, per-group rank sums.

Required fields:
- `field` — numeric value
- `split_by` — categorical producing k ≥ 2 groups

Output Details:
- `groups`, `n` (per group), `rank_sums`
- `n_total`, `tie_factor` — H is tie-corrected by `1 − Σ(t³−t)/(N³−N)`

Streamable: no.

### TEST_PEARSON_R — parametric correlation

Both tiers supported.

Tier-1 (`Request.Tests`): Welford cross-product accumulator over the raw row stream. Streamable; reuses online (mean, M2) moments and adds a `C = Σ Δx · (y − mean_y)` term. The correlation coefficient `r = C / √(M2_x · M2_y)` is exact to float64 precision; `t = r · √((n−2)/(1−r²))` drives a two-sided p-value via the Student-t survival function. Confidence interval uses the Fisher z-transform with SE = 1/√(n−3). Variant: `pearson`.

Tier-2 (`Request.PostTests`): same math, but `field` and `field2` reference columns on the materialized result row set (aggregator labels, attribute labels, window outputs). Useful for correlating two per-group aggregates (e.g. `AGG_SUM_revenue` vs `AGG_AVERAGE_basket_size` across regions) or two windowed series. Variant: `pearson_post` — consumers can distinguish raw-row r from aggregate r by inspecting the result's `Variant` field. **Caveat — ecological correlation:** r on per-group aggregates answers a fundamentally different question than r on raw rows and can disagree markedly (Simpson's paradox, ecological fallacy). Prefer the tier-1 variant for "is x correlated with y at the individual level"; use the tier-2 variant when the question is genuinely about group-level relationships.

Required fields:
- `field` — numeric column 1
- `field2` — numeric column 2

Output Details (both tiers):
- `n`, `t` — sample size and t-statistic (df = n − 2)
- `ci_low`, `ci_high` — Fisher-z two-sided CI for r
- `mean_x`, `mean_y`, `variance_x`, `variance_y`, `covariance`

Errors:
- `PULSE_TEST_INSUFFICIENT_N` — n < 3
- `PULSE_TEST_CORRELATION_UNDEFINED` — at least one column has zero variance

Streamable: yes (tier-1); tier-2 is always buffered by definition.

### TEST_SPEARMAN_R — rank-based correlation

Both tiers supported. Buffered: mid-rank each column independently, then Pearson on the ranks. Tier-2 variant `rank_pearson_post` operates on result-row columns.

Required fields:
- `field` — numeric column 1
- `field2` — numeric column 2

Output Details:
- `n`, `t` — t-statistic and df = n−2
- `ties_x`, `ties_y` — per-column tie-group sizes

Streamable: no.

### TEST_ANOVA_WELCH — heteroscedasticity-robust one-way ANOVA

Both tiers supported. Tier-1 is a streaming variant of `TEST_ANOVA_F` that does not assume equal variances; per-group online Welford state matches the standard ANOVA, finalization uses the Welch (1951) weighting `w_i = n_i / s²_i` with the Welch-Satterthwaite df₂ correction.

Tier-2 variant `welch_one_way_post` consumes per-group summary stats from result-row columns and applies the same Welch finalization. Required `params.n_col` and `params.variance_col` columns mirror the `TEST_ANOVA_F` post contract.

Required fields:
- `field` — numeric value
- `split_by` — categorical defining k ≥ 2 groups

Output Details:
- `groups`, `n` (per group), `group_means`, `group_variances`, `weights`
- `weighted_mean`, `df_between`, `df_within`

Streamable: yes. Use when group variances are visibly unequal (Brown-Forsythe rejects, or sample variances differ by > ~3×).

### TEST_ANOVA_RM — one-way repeated-measures ANOVA

Buffered. Balanced design only (one observation per subject per condition). Decomposes SS into between-subject, treatment, and error components; `F = MS_treatment / MS_error` with `df = (k−1, (n−1)(k−1))`.

Required fields:
- `field` — numeric value column
- `split_by` — categorical condition column (within-subject factor)
- `subject_field` — categorical subject identifier

Output Details:
- `conditions`, `condition_means`, `grand_mean`
- `complete_subjects`, `dropped_subjects`
- `ss_total`, `ss_between_subjects`, `ss_treatment`, `ss_error`, `df_treatment`, `df_error`

Sphericity correction (Greenhouse-Geisser / Huynh-Feldt) is documented as future work — current variant reports the uncorrected F.

Streamable: no.

### TEST_FISHER_EXACT — Fisher's exact test (2×2)

Tier-1 buffered. Exact two-sided p-value for a 2×2 contingency table by enumerating every hypergeometric outcome at the observed marginals and summing probabilities ≤ the observed table.

Required fields:
- `rows` — categorical row axis (2 levels)
- `cols` — categorical column axis (2 levels)

Output Details:
- `row_labels`, `col_labels`
- `contingency` — observed 2×2 table
- `odds_ratio`, `n`

Use case: backstop for `TEST_CHISQ` when any expected cell < 5.

Streamable: no.

### TEST_SHAPIRO_WILK — normality test

Both tiers supported. Tier-1 buffered. Shapiro-Francia variant (Royston 1993): mid-rank-style coefficients on the Blom-approximated expected normal order statistics. p-value via the standard Royston polynomial transform of `W'`. Tier-2 variant `shapiro_francia_post` runs identical math on a result column, optionally per-group via `split_by`.

Required fields:
- `field` — numeric value
- `split_by` (optional) — categorical; when set, the test runs per group and reports per-group W and p in Details

Output Details (single-bucket):
- `per_group[].n`, `per_group[].w`, `per_group[].z`, `per_group[].p_value`

Headline `Statistic` / `PValue` track the worst (smallest p) across groups.

Caveat: n ≤ 5000 is the supported range; larger samples surface `PULSE_TEST_SHAPIRO_N_BOUND` warnings.

Streamable: no.

### TEST_BROWN_FORSYTHE — variance homogeneity (median-based)

Both tiers supported. Buffered. Replaces each value with its absolute deviation from the per-group median, then runs one-way ANOVA on the deviations. Robust against non-normality; the conventional preferred variant over Levene's mean-based residuals. Tier-2 variant `median_post` consumes result rows (raw values, not pre-aggregated summary stats — input is a flat row stream).

Required fields:
- `field` — numeric value
- `split_by` — categorical defining k ≥ 2 groups

Output Details:
- `groups`, `n`, `group_medians`, `abs_dev_means`
- `ss_between`, `ss_within`, `df_between`, `df_within`

Use case: pre-ANOVA gate. If Brown-Forsythe rejects, prefer `TEST_ANOVA_WELCH` over `TEST_ANOVA_F`.

Streamable: no.

### TEST_KENDALL_TAU — concordance-based correlation

Both tiers supported. τ-b variant with tie correction. Buffered O(n²) pair count. Tier-2 variant `tau_b_post` operates on result-row columns.

Required fields:
- `field` — numeric column 1
- `field2` — numeric column 2

Output Details:
- `n`, `concordant`, `discordant`, `ties_x`, `ties_y`
- `s`, `var_s`, `z` — Kendall S statistic and its asymptotic variance under the null

Streamable: no.

## Streamability summary

| Test | Tier-1 streamable | Notes |
|---|---|---|
| `TEST_T` | yes | reuses online μ, σ², n per split bucket |
| `TEST_WELCH` | yes | alias of `TEST_T` two-sample |
| `TEST_Z_TWO_SAMPLE` | yes | same Welford state as `TEST_WELCH`, p-value via Φ |
| `TEST_CHISQ` | yes | online contingency counts |
| `TEST_ANOVA_F` | yes | online per-group moments |
| `TEST_PEARSON_R` | yes | online cross-product Welford |
| `TEST_PAIRED_T` | yes | one-sample Welford on per-row diff |
| `TEST_PROP_Z` | yes | per-group success/total counts |
| `TEST_KS` | no | needs sorted ECDF |
| `TEST_TUKEY_HSD` | tier-2 only | runs over result rows |
| `TEST_TREND` | no | needs ordered full series |
| `TEST_MANN_WHITNEY_U` | no | combined-set ranks |
| `TEST_WILCOXON_SR` | no | sort of \|diff\| |
| `TEST_KRUSKAL_WALLIS` | no | combined-set ranks |
| `TEST_SPEARMAN_R` | no | rank both columns |
| `TEST_KENDALL_TAU` | no | O(n²) pair count |
| `TEST_ANOVA_WELCH` | yes | online per-group moments + Welch weighting |
| `TEST_ANOVA_RM` | no | wide pivot over subject × condition |
| `TEST_BROWN_FORSYTHE` | no | per-group medians require a sort |
| `TEST_FISHER_EXACT` | no | hypergeometric enumeration on full table |
| `TEST_SHAPIRO_WILK` | no | sort + Blom coefficients on full sample |

Tier-2 (`post_tests`) always runs over the materialized result set after windows, regardless of test type.

## Implementation status

| Test | Tier-1 row test | Tier-2 post test |
|---|---|---|
| `TEST_T` | ✓ | — |
| `TEST_WELCH` | ✓ (alias of two-sample `TEST_T`) | — |
| `TEST_Z_TWO_SAMPLE` | ✓ (Welch SE, normal-CDF p) | — |
| `TEST_CHISQ` | ✓ | — |
| `TEST_ANOVA_F` | ✓ | ✓ (from summary stats) |
| `TEST_KS` | ✓ (forces buffered path) | ✓ (variant `two_sample_post`) |
| `TEST_PAIRED_T` | ✓ | ✓ (variant `paired_two_sided_post`, one-sample t on per-row diff) |
| `TEST_PROP_Z` | ✓ | — |
| `TEST_PEARSON_R` | ✓ | ✓ (variant `pearson_post`, Welford cross-product on result columns) |
| `TEST_MANN_WHITNEY_U` | ✓ (forces buffered path) | — |
| `TEST_WILCOXON_SR` | ✓ (forces buffered path) | ✓ (variant `asymptotic_post`, mid-rank \|diff\| with tie correction) |
| `TEST_KRUSKAL_WALLIS` | ✓ (forces buffered path) | — |
| `TEST_SPEARMAN_R` | ✓ (forces buffered path) | ✓ (variant `rank_pearson_post`, mid-rank then Pearson on ranks) |
| `TEST_KENDALL_TAU` | ✓ (forces buffered path) | ✓ (variant `tau_b_post`, O(n²) concordance count) |
| `TEST_ANOVA_WELCH` | ✓ | ✓ (variant `welch_one_way_post`, from summary stats) |
| `TEST_ANOVA_RM` | ✓ (forces buffered path) | — |
| `TEST_BROWN_FORSYTHE` | ✓ (forces buffered path) | ✓ (variant `median_post`, median-based ANOVA on |dev|) |
| `TEST_TREND` | — | ✓ (Mann-Kendall) |
| `TEST_TUKEY_HSD` | — | ✓ (Tukey-Kramer with studentized-range p-values) |
| `TEST_FISHER_EXACT` | ✓ (forces buffered path) | — |
| `TEST_SHAPIRO_WILK` | ✓ (forces buffered path) | ✓ (variant `shapiro_francia_post`, optional `split_by` for per-group W) |

## Validation rules (predict)

- `alpha` must lie in (0, 1). Default is 0.05 when zero.
- `TEST_T` / `TEST_WELCH` / `TEST_ANOVA_F` / `TEST_KS` / `TEST_TREND` require `field` to be numeric.
- `TEST_CHISQ` requires `rows` *and* `cols` — both categorical. `field` is ignored.
- Two-sample variants need a `split_by` field. ANOVA accepts ≥ 2 groups; two-sample t-tests expect exactly 2.
- Tier-2 tests reference column names produced by upstream stages (aggregator labels, attribute labels, window output columns, grouper keys). Predict resolves these against the predicted result schema.

## Error codes

| Code | When |
|---|---|
| `PULSE_TEST_UNKNOWN_TYPE` | unrecognized `TestType` |
| `PULSE_TEST_FIELD_NOT_NUMERIC` | numeric test references a non-numeric field |
| `PULSE_TEST_INVALID_ALPHA` | alpha outside (0, 1) |
| `PULSE_TEST_INSUFFICIENT_N` | per-group N below minimum |
| `PULSE_TEST_VARIANCE_ZERO` | constant field within a group |
| `PULSE_TEST_SPLIT_GROUPS_LT_2` | fewer split groups than the test requires |
| `PULSE_TEST_CONTINGENCY_DEGENERATE` | chi-square table empty or 1-row/1-col |
| `PULSE_TEST_EXPECTED_COUNT_TOO_LOW` | warning — chi-square expected cell < 5 |
| `PULSE_TEST_PAIRED_LENGTH_MISMATCH` | warning — paired test dropped rows with one-sided nulls |
| `PULSE_TEST_TIES_DOMINATE` | warning — ≥ 50 % of ranked values are tied |
| `PULSE_TEST_SUBJECT_MISSING` | warning — RM-ANOVA dropped subjects with incomplete conditions |
| `PULSE_TEST_BALANCED_DESIGN_REQUIRED` | RM-ANOVA observed unequal cell counts |
| `PULSE_TEST_TUKEY_REQUIRES_K_GE_3` | Tukey HSD on fewer than 3 groups |
| `PULSE_TEST_SHAPIRO_N_BOUND` | warning — Shapiro-Wilk n above 5000 |
| `PULSE_TEST_FISHER_R_OR_C_GT_2` | Fisher exact on tables larger than 2×2 |

See `error-code-reference.md` for recovery steps.

## Examples

### A/B test on revenue

```json
{
  "cohort": {"filename": "sales.pulse"},
  "tests": [
    {"type": "TEST_T", "field": "revenue", "split_by": "treatment", "alpha": 0.05, "label": "rev_ttest"}
  ]
}
```

### Aggregate + tier-1 + tier-2 in one request

```json
{
  "cohort": {"filename": "sales.pulse"},
  "groupers": [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregators": [
    {"type": "AGG_AVERAGE", "field": "revenue", "alias": "avg_revenue"},
    {"type": "AGG_COUNT", "alias": "n"}
  ],
  "tests": [
    {"type": "TEST_CHISQ", "rows": "region", "cols": "churned"}
  ],
  "post_tests": [
    {"type": "TEST_ANOVA_F", "field": "avg_revenue", "split_by": "region"}
  ]
}
```

### Post-hoc after ANOVA

```json
{
  "post_tests": [
    {"type": "TEST_ANOVA_F", "field": "avg_revenue", "split_by": "region", "label": "anova"},
    {"type": "TEST_TUKEY_HSD", "field": "avg_revenue", "split_by": "region",
     "params": {"ms_within": 1816.78, "df_within": 19496}}
  ]
}
```

Run a `TEST_ANOVA_F` first, read `ms_within` / `df_within` from its Details, then issue a second request with `TEST_TUKEY_HSD`. Future iteration may chain these automatically.

### Tier-2 Pearson correlation between per-group aggregates

```json
{
  "cohort": {"filename": "orders.pulse"},
  "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
  "aggregations": [
    {"type": "AGG_SUM",     "field": "revenue", "alias": "total_revenue"},
    {"type": "AGG_AVERAGE", "field": "basket",  "alias": "avg_basket"}
  ],
  "post_tests": [
    {
      "type": "TEST_PEARSON_R",
      "field":  "AGG_SUM_revenue",
      "field2": "AGG_AVERAGE_basket",
      "alpha":  0.05,
      "label":  "rev_basket_corr"
    }
  ]
}
```

Field names match the aggregator's projected column (`AGG_<TYPE>_<field>`) since aliases are not yet honored in the output schema. Result's `Variant` is `pearson_post` (tier-1 is `pearson`).

### Tier-2 paired t-test on pre/post columns

```json
{
  "post_tests": [
    {"type": "TEST_PAIRED_T", "field": "post_mean", "field2": "pre_mean", "label": "lift_t"}
  ]
}
```

Tests the per-row mean lift `(post − pre)` against zero. Useful when each result row carries a pre/post pair (e.g. before/after windows or two aggregations across the same group). `TEST_WILCOXON_SR` is the nonparametric equivalent with the same field shape.

### Tier-2 Welch ANOVA from per-group summary stats

```json
{
  "post_tests": [
    {
      "type":    "TEST_ANOVA_WELCH",
      "field":   "AGG_AVERAGE_revenue",
      "split_by": "region",
      "params":  {"n_col": "AGG_COUNT_id", "variance_col": "AGG_VARIANCE_revenue"}
    }
  ]
}
```

Use when per-group variances are visibly unequal (run `TEST_BROWN_FORSYTHE` first as the gate). Requires upstream aggregators emitting count and variance columns alongside the mean.

### Tier-2 normality + variance-homogeneity diagnostics

```json
{
  "post_tests": [
    {"type": "TEST_SHAPIRO_WILK",   "field": "AGG_AVERAGE_revenue", "split_by": "region"},
    {"type": "TEST_BROWN_FORSYTHE", "field": "revenue",             "split_by": "region"}
  ]
}
```

Useful as a pre-flight before tier-2 ANOVA. Shapiro-Wilk reports per-group normality (headline `Statistic` / `PValue` track the worst group); Brown-Forsythe gates the ANOVA choice (reject ⇒ use Welch).
