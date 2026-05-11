---
name: statistical-testing
description: TEST_* operators — tier 1 row tests and tier 2 post tests in one Process pipeline
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
| Independence of two categorical variables | 1 | `TEST_CHISQ` on `region × churned` |
| Compare means across k categories on raw rows | 1 | `TEST_ANOVA_F` on `revenue` split by `region` |
| Distribution comparison (CDF) | 1 | `TEST_KS` two-sample, raw rows |
| Compare aggregated per-group means | 2 | `TEST_ANOVA_F` on `avg_revenue` per `region` row |
| Pairwise comparison after a significant ANOVA | 2 | `TEST_TUKEY_HSD` on per-group means |
| Trend over an ordered series | 2 | `TEST_TREND` (Mann-Kendall) on `moving_avg_revenue` ordered by `period` |

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

## Streamability summary

| Test | Tier-1 streamable | Notes |
|---|---|---|
| `TEST_T` | yes | reuses online μ, σ², n per split bucket |
| `TEST_WELCH` | yes | alias of `TEST_T` two-sample |
| `TEST_CHISQ` | yes | online contingency counts |
| `TEST_ANOVA_F` | yes | online per-group moments |
| `TEST_KS` | no | needs sorted ECDF |
| `TEST_TUKEY_HSD` | tier-2 only | runs over result rows |
| `TEST_TREND` | no | needs ordered full series |

Tier-2 (`post_tests`) always runs over the materialized result set after windows, regardless of test type.

## Implementation status

| Test | Tier-1 row test | Tier-2 post test |
|---|---|---|
| `TEST_T` | ✓ | — |
| `TEST_WELCH` | ✓ (alias of two-sample `TEST_T`) | — |
| `TEST_CHISQ` | ✓ | — |
| `TEST_ANOVA_F` | ✓ | ✓ (from summary stats) |
| `TEST_KS` | ✓ (forces buffered path) | — |
| `TEST_PAIRED_T` | ✓ | — |
| `TEST_PROP_Z` | ✓ | — |
| `TEST_PEARSON_R` | ✓ | — |
| `TEST_TREND` | — | ✓ (Mann-Kendall) |
| `TEST_TUKEY_HSD` | — | — (declared in enum; studentized-range distribution lands in a follow-up. Requests surface `PULSE_TEST_UNKNOWN_TYPE`.) |

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
